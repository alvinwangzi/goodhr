// 本文件将 HRPlus 复打发送确认与待补传收据放在同一 SQLite 事务，补传无需浏览器执行权。
package localdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ReGreetOutbox 保存不可变发送事实及独立重试安排，不保存登录 Token。
type ReGreetOutbox struct {
	OperationID  string
	ProfileScope string
	Platform     string
	CandidateID  string
	APIBase      string
	Payload      json.RawMessage
	Status       string
	Attempts     int
	NextRetryAt  time.Time
}

// migrateReGreetOutbox 创建 M1 原子补传表，每个字段保留中文用途说明。
func (db *DB) migrateReGreetOutbox() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS re_greet_outbox (
 -- 原发送意图编号，不能因网络重试变化。
 operation_id TEXT PRIMARY KEY REFERENCES auto_reply_records(id),
 -- 实际账号作用域摘要。
 profile_scope TEXT NOT NULL,
 -- 招聘平台标识。
 platform TEXT NOT NULL,
 -- 候选人完整平台标识。
 candidate_id TEXT NOT NULL,
 -- 原云端接口地址，不保存凭证。
 api_base TEXT NOT NULL,
 -- 原始发送收据事实，不包含登录 Token。
 payload TEXT NOT NULL,
 -- pending 等待补传、acked 已确认、auth_required 等待登录、conflict 待核对。
 status TEXT NOT NULL CHECK(status IN ('pending','acked','auth_required','conflict')),
 -- 已尝试补传次数。
 attempts INTEGER NOT NULL DEFAULT 0,
 -- 持久化下一次允许补传时间。
 next_retry_at TEXT NOT NULL,
 -- 最近错误类别代码，不存原始响应和聊天内容。
 error_code TEXT NOT NULL DEFAULT '',
 -- 首次确认发送的记录时间。
 created_at TEXT NOT NULL,
 -- 最近结果更新时间。
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS re_greet_outbox_due ON re_greet_outbox(status,next_retry_at);`)
	return err
}

// ConfirmReGreetAndQueue 原子更新 sending 为 sent 并创建补传结果，事务失败时两者都回滚。
func (db *DB) ConfirmReGreetAndQueue(ctx context.Context, entry ReGreetOutbox) error {
	var facts struct {
		OperationID string `json:"operation_id"`
		CandidateID string `json:"platform_candidate_id"`
	}
	if err := json.Unmarshal(entry.Payload, &facts); err != nil {
		return err
	}
	if entry.OperationID == "" || entry.ProfileScope == "" || entry.Platform == "" || entry.CandidateID == "" || entry.APIBase == "" || facts.OperationID != entry.OperationID || facts.CandidateID != entry.CandidateID {
		return fmt.Errorf("复打补传结果身份不完整或不一致")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE auto_reply_records SET status='sent',error_code='',updated_at=? WHERE id=? AND profile_scope=? AND platform=? AND status='sending'`, now, entry.OperationID, entry.ProfileScope, entry.Platform)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAutoReplyConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO re_greet_outbox(operation_id,profile_scope,platform,candidate_id,api_base,payload,status,attempts,next_retry_at,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',0,?,?,?)`, entry.OperationID, entry.ProfileScope, entry.Platform, entry.CandidateID, entry.APIBase, string(entry.Payload), now, now, now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// PendingReGreetForCandidate 阻止仍待确认上报的候选人创建下一次复打意图。
func (db *DB) PendingReGreetForCandidate(ctx context.Context, scope, platform, candidateID string) (bool, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM re_greet_outbox WHERE profile_scope=? AND platform=? AND candidate_id=? AND status<>'acked'`, scope, platform, candidateID).Scan(&count)
	return count > 0, err
}

// DueReGreetOutbox 读取当前登录作用域的到期队列，不跨账号或不明登录补传。
func (db *DB) DueReGreetOutbox(ctx context.Context, scope string, now time.Time, limit int) ([]ReGreetOutbox, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT operation_id,profile_scope,platform,candidate_id,api_base,payload,status,attempts,next_retry_at FROM re_greet_outbox WHERE profile_scope=? AND status='pending' ORDER BY next_retry_at,operation_id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []ReGreetOutbox{}
	for rows.Next() {
		var entry ReGreetOutbox
		var payload, rawTime string
		if err := rows.Scan(&entry.OperationID, &entry.ProfileScope, &entry.Platform, &entry.CandidateID, &entry.APIBase, &payload, &entry.Status, &entry.Attempts, &rawTime); err != nil {
			return nil, err
		}
		entry.Payload = json.RawMessage(payload)
		entry.NextRetryAt, err = time.Parse(time.RFC3339Nano, rawTime)
		if err != nil {
			return nil, err
		}
		if entry.NextRetryAt.After(now) {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}
	return entries, rows.Err()
}

// FinishReGreetUpload 更新补传结果，原次数用于防止迟到的失败覆盖已确认收据。
func (db *DB) FinishReGreetUpload(ctx context.Context, entry ReGreetOutbox, status, errorCode string, now time.Time) error {
	if status != "pending" && status != "acked" && status != "auth_required" && status != "conflict" {
		return fmt.Errorf("复打补传状态不支持")
	}
	next := now.Add(ReGreetRetryDelay(entry.Attempts))
	result, err := db.conn.ExecContext(ctx, `UPDATE re_greet_outbox SET status=?,attempts=attempts+1,next_retry_at=?,error_code=?,updated_at=? WHERE operation_id=? AND profile_scope=? AND status='pending' AND attempts=?`, status, next.UTC().Format(time.RFC3339Nano), errorCode, now.UTC().Format(time.RFC3339Nano), entry.OperationID, entry.ProfileScope, entry.Attempts)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAutoReplyConflict
	}
	return nil
}

// ReGreetRetryDelay 返回固定退避，重启使用已保存 next_retry_at，不重新抽样。
func ReGreetRetryDelay(attempts int) time.Duration {
	switch attempts {
	case 0:
		return 5 * time.Second
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// ResumeReGreetUploads 在同账号重新登录后恢复待补传，不改变原发送编号和事实。
func (db *DB) ResumeReGreetUploads(ctx context.Context, scope string) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE re_greet_outbox SET status='pending',next_retry_at=? WHERE profile_scope=? AND status='auth_required'`, time.Now().UTC().Format(time.RFC3339Nano), scope)
	return err
}

// HasRetryableReGreetUploads 判断作用域是否仍有可以等待重试的收据，冲突和等待登录不轮询。
func (db *DB) HasRetryableReGreetUploads(ctx context.Context, scope string) (bool, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM re_greet_outbox WHERE profile_scope=? AND status='pending'`, scope).Scan(&count)
	return count > 0, err
}
