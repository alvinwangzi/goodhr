// 本文件原子保存 HRPlus 复打收据、联系事实与业务事件，重复补传不会再次计数。
package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrReGreetReceiptConflict 表示编号内容或联系基准冲突，需要人工核对。
var ErrReGreetReceiptConflict = errors.New("复打收据或联系基准冲突，需要核对")

// ReGreetReceiptInput 保存发送时固定的事实，重试不修改其内容。
type ReGreetReceiptInput struct {
	OperationID   string    `json:"operation_id"`
	OwnerEmail    string    `json:"owner_email"`
	PositionID    string    `json:"position_id"`
	Platform      string    `json:"platform"`
	CandidateID   string    `json:"candidate_id"`
	RunID         string    `json:"run_id"`
	BaseCount     int       `json:"base_count"`
	BaseContactAt time.Time `json:"base_contact_at"`
	SentAt        time.Time `json:"sent_at"`
	MessageText   string    `json:"message_text"`
}

// ReGreetReceipt 是重复请求返回的原记账结果，不用当前累计次数代替。
type ReGreetReceipt struct {
	OperationID string    `json:"operation_id"`
	ResultCount int       `json:"result_count"`
	SentAt      time.Time `json:"sent_at"`
	ReceivedAt  time.Time `json:"received_at"`
}

// ReGreetReceiptStore 是兼容旧扫描存储的可选可靠记账能力。
type ReGreetReceiptStore interface {
	CommitReGreetReceipt(context.Context, ReGreetReceiptInput) (ReGreetReceipt, error)
}

// receiptDigest 校验并规范化发送事实，摘要不包含收到请求或重试时间。
func receiptDigest(in ReGreetReceiptInput) (string, error) {
	if strings.TrimSpace(in.OperationID) == "" || len(in.OperationID) > 128 || in.OwnerEmail == "" || in.PositionID == "" || in.Platform == "" || in.CandidateID == "" || in.BaseCount < 0 || in.BaseContactAt.IsZero() || in.SentAt.Before(in.BaseContactAt) || in.SentAt.After(time.Now().Add(5*time.Minute)) || strings.TrimSpace(in.MessageText) == "" || utf8.RuneCountInString(in.MessageText) > 200 {
		return "", fmt.Errorf("复打收据缺少合法身份、基准或发送事实")
	}
	in.BaseContactAt = in.BaseContactAt.UTC()
	in.SentAt = in.SentAt.UTC()
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// CommitReGreetReceipt 在 PostgreSQL 同一事务内核对原请求、锁候选人、更新次数及写入事件和日志。
func (s *PostgresCandidateScreeningStore) CommitReGreetReceipt(ctx context.Context, in ReGreetReceiptInput) (ReGreetReceipt, error) {
	digest, err := receiptDigest(in)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	defer tx.Rollback()
	// 同一编号先串行化，避免它被并发用于两个候选人；候选人行锁保护换编号重复基准。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "re_greet_receipt:"+in.OperationID); err != nil {
		return ReGreetReceipt{}, err
	}
	result := ReGreetReceipt{OperationID: in.OperationID}
	var storedDigest string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,result_count,sent_at,received_at FROM re_greet_receipts WHERE operation_id=$1`, in.OperationID).Scan(&storedDigest, &result.ResultCount, &result.SentAt, &result.ReceivedAt)
	if err == nil {
		if storedDigest != digest {
			return ReGreetReceipt{}, ErrReGreetReceiptConflict
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReGreetReceipt{}, err
	}
	var count int
	var basis sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT re_greet_count,COALESCE(last_re_greeted_at,greeted_at) FROM candidate_screenings WHERE position_id=$1 AND platform=$2 AND platform_candidate_id=$3 FOR UPDATE`, in.PositionID, in.Platform, in.CandidateID).Scan(&count, &basis)
	if errors.Is(err, sql.ErrNoRows) {
		return ReGreetReceipt{}, ErrNotFound
	}
	if err != nil {
		return ReGreetReceipt{}, err
	}
	// PostgreSQL 时间精度为微秒，客户端基准来源为原查询结果，不接受旧联系事实。
	if !basis.Valid || count != in.BaseCount || !basis.Time.Equal(in.BaseContactAt) {
		return ReGreetReceipt{}, ErrReGreetReceiptConflict
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO re_greet_receipts(operation_id,owner_email,position_id,platform,candidate_id,request_digest,base_count,base_contact_at,sent_at,result_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING sent_at,received_at`, in.OperationID, in.OwnerEmail, in.PositionID, in.Platform, in.CandidateID, digest, in.BaseCount, in.BaseContactAt, in.SentAt, count+1).Scan(&result.SentAt, &result.ReceivedAt)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	result.ResultCount = count + 1
	if _, err = tx.ExecContext(ctx, `UPDATE candidate_screenings SET re_greet_count=$4,last_re_greeted_at=$5,updated_at=now() WHERE position_id=$1 AND platform=$2 AND platform_candidate_id=$3`, in.PositionID, in.Platform, in.CandidateID, result.ResultCount, in.SentAt); err != nil {
		return ReGreetReceipt{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"source": "re_greet", "operation_id": in.OperationID, "platform_candidate_id": in.CandidateID})
	_, err = tx.ExecContext(ctx, `INSERT INTO candidate_events(tenant_id,candidate_id,engagement_id,task_id,position_id,platform_id,event_type,message_text,metadata,created_at)
 SELECT e.tenant_id,e.candidate_id,e.id,NULLIF($4,'')::uuid,e.position_id,e.platform_id,'re_greeted_sent',$5,$6::jsonb,$7
 FROM candidate_engagements e JOIN candidate_profiles p ON p.id=e.candidate_id WHERE e.position_id=$1 AND p.source_platform_id=$2 AND p.source_platform_candidate_id=$3`, in.PositionID, in.Platform, in.CandidateID, in.RunID, in.MessageText, string(metadata), in.SentAt)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO position_logs(position_id,user_id,level,message,created_at) SELECT p.id,p.user_id,'info',$2,$3 FROM positions p WHERE p.id=$1`, in.PositionID, "复打发送已确认，收据="+in.OperationID, in.SentAt)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	return result, tx.Commit()
}

// CommitReGreetReceipt 在内存版同一互斥区间内保存收据和联系事实，供独立契约测试使用。
func (s *MemoryCandidateScreeningStore) CommitReGreetReceipt(_ context.Context, in ReGreetReceiptInput) (ReGreetReceipt, error) {
	digest, err := receiptDigest(in)
	if err != nil {
		return ReGreetReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receipts == nil {
		s.receipts = map[string]memoryReGreetReceipt{}
	}
	if old, ok := s.receipts[in.OperationID]; ok {
		if old.digest != digest {
			return ReGreetReceipt{}, ErrReGreetReceiptConflict
		}
		return old.receipt, nil
	}
	item, ok := s.items[in.PositionID+"|"+in.Platform+"|"+in.CandidateID]
	if !ok {
		return ReGreetReceipt{}, ErrNotFound
	}
	basis := item.LastReGreetedAt
	if basis == nil {
		basis = item.GreetedAt
	}
	if basis == nil || item.ReGreetCount != in.BaseCount || !basis.Equal(in.BaseContactAt) {
		return ReGreetReceipt{}, ErrReGreetReceiptConflict
	}
	item.ReGreetCount++
	sent := in.SentAt
	item.LastReGreetedAt = &sent
	item.UpdatedAt = time.Now().UTC()
	receipt := ReGreetReceipt{OperationID: in.OperationID, ResultCount: item.ReGreetCount, SentAt: sent, ReceivedAt: item.UpdatedAt}
	s.receipts[in.OperationID] = memoryReGreetReceipt{digest: digest, receipt: receipt}
	return receipt, nil
}

// memoryReGreetReceipt 保存不可变摘要和原返回结果，不复用当前候选人计数。
type memoryReGreetReceipt struct {
	digest  string
	receipt ReGreetReceipt
}
