// 本文件作用：管理本地"待索要简历名单"（resume_request_queue 表）的写入与状态流转，
// 支持打招呼后先记名单、岗位收尾时统一检查回复再索要简历的新流程。
package localdb

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// 待索要名单记录的状态值。
const (
	// ResumeRequestStatusPending 表示候选人已打招呼，等待检查其是否回复。
	ResumeRequestStatusPending = "pending"
	// ResumeRequestStatusRequested 表示已确认候选人回复并完成索要动作。
	ResumeRequestStatusRequested = "requested"
	// ResumeRequestStatusFailed 表示本轮检查未能完成索要（如会话列表中未找到）。
	ResumeRequestStatusFailed = "failed"
)

// ResumeRequest 表示待索要简历名单中的一条候选人记录。
type ResumeRequest struct {
	ID            string `json:"id"`             // 记录唯一 ID
	PositionID    string `json:"position_id"`    // 关联岗位运行 ID
	PlatformID    string `json:"platform_id"`    // 平台 ID，如 boss
	CandidateName string `json:"candidate_name"` // 候选人姓名
	Status        string `json:"status"`         // 状态：pending/requested/failed
	FailReason    string `json:"fail_reason"`    // 失败原因
	CreatedAt     string `json:"created_at"`     // 入队时间
	UpdatedAt     string `json:"updated_at"`     // 更新时间
	RequestedAt   string `json:"requested_at"`   // 完成索要时间
}

// EnqueueResumeRequest 把候选人写入待索要名单。
// 同岗位同名候选人已存在时直接复用现有记录：已完成的保持不变，失败的重置为待检查，给下一次收尾再查一次的机会。
// positionID 为岗位运行 ID，platformID 为平台 ID，candidateName 为候选人姓名，返回写入或复用的记录。
func (db *DB) EnqueueResumeRequest(positionID string, platformID string, candidateName string) (ResumeRequest, error) {
	name := strings.TrimSpace(candidateName)
	if strings.TrimSpace(positionID) == "" || name == "" {
		return ResumeRequest{}, fmt.Errorf("写入待索要名单失败：岗位或候选人姓名为空")
	}
	existing, err := db.findResumeRequest(positionID, name)
	if err != nil {
		return ResumeRequest{}, err
	}
	now := nowISO()
	if existing.ID != "" {
		if existing.Status != ResumeRequestStatusFailed {
			return existing, nil
		}
		if _, err := db.conn.Exec(`
UPDATE resume_request_queue
SET status=?, fail_reason='', updated_at=?
WHERE id=?`, ResumeRequestStatusPending, now, existing.ID); err != nil {
			return ResumeRequest{}, fmt.Errorf("重置待索要名单记录失败：%w", err)
		}
		existing.Status = ResumeRequestStatusPending
		existing.FailReason = ""
		existing.UpdatedAt = now
		return existing, nil
	}
	item := ResumeRequest{
		ID:            uuid.NewString(),
		PositionID:    positionID,
		PlatformID:    strings.TrimSpace(platformID),
		CandidateName: name,
		Status:        ResumeRequestStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err = db.conn.Exec(`
INSERT INTO resume_request_queue (
    id, position_id, platform_id, candidate_name, status, fail_reason, created_at, updated_at, requested_at
) VALUES (?, ?, ?, ?, ?, '', ?, ?, '')`,
		item.ID, item.PositionID, item.PlatformID, item.CandidateName,
		item.Status, item.CreatedAt, item.UpdatedAt,
	)
	if err != nil {
		return ResumeRequest{}, fmt.Errorf("写入待索要名单失败：%w", err)
	}
	return item, nil
}

// ListResumeRequests 按岗位读取待索要名单。
// positionID 为空时返回全部岗位的记录，仅返回指定状态时用 status 过滤，传空返回全部状态。
func (db *DB) ListResumeRequests(positionID string, status string) ([]ResumeRequest, error) {
	query := `SELECT id, position_id, platform_id, candidate_name, status, fail_reason, created_at, updated_at, requested_at FROM resume_request_queue`
	args := []any{}
	clauses := []string{}
	if strings.TrimSpace(positionID) != "" {
		clauses = append(clauses, "position_id=?")
		args = append(args, positionID)
	}
	if strings.TrimSpace(status) != "" {
		clauses = append(clauses, "status=?")
		args = append(args, status)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at ASC"
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取待索要名单失败：%w", err)
	}
	defer rows.Close()
	result := []ResumeRequest{}
	for rows.Next() {
		item, scanErr := scanResumeRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// MarkResumeRequested 把名单记录标记为已完成索要。
// id 为记录 ID，完成索要时间按当前时间写入。
func (db *DB) MarkResumeRequested(id string) error {
	now := nowISO()
	result, err := db.conn.Exec(`
UPDATE resume_request_queue
SET status=?, requested_at=?, updated_at=?
WHERE id=?`, ResumeRequestStatusRequested, now, now, id)
	if err != nil {
		return fmt.Errorf("标记索要完成失败：%w", err)
	}
	return requireResumeRequestRow(result, id)
}

// MarkResumeFailed 把名单记录标记为失败并记录原因。
// id 为记录 ID，reason 为失败原因；失败记录在候选人再次打招呼时会被重置为待检查。
func (db *DB) MarkResumeFailed(id string, reason string) error {
	now := nowISO()
	result, err := db.conn.Exec(`
UPDATE resume_request_queue
SET status=?, fail_reason=?, updated_at=?
WHERE id=?`, ResumeRequestStatusFailed, strings.TrimSpace(reason), now, id)
	if err != nil {
		return fmt.Errorf("标记索要失败失败：%w", err)
	}
	return requireResumeRequestRow(result, id)
}

// findResumeRequest 按岗位和候选人姓名查找现有名单记录，没有时返回零值记录。
func (db *DB) findResumeRequest(positionID string, candidateName string) (ResumeRequest, error) {
	rows, err := db.conn.Query(`
SELECT id, position_id, platform_id, candidate_name, status, fail_reason, created_at, updated_at, requested_at
FROM resume_request_queue
WHERE position_id=? AND candidate_name=?
LIMIT 1`, positionID, candidateName)
	if err != nil {
		return ResumeRequest{}, fmt.Errorf("查询待索要名单失败：%w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ResumeRequest{}, rows.Err()
	}
	return scanResumeRequest(rows)
}

// scanResumeRequest 从查询结果读取一条名单记录。
func scanResumeRequest(rows *sql.Rows) (ResumeRequest, error) {
	var item ResumeRequest
	if err := rows.Scan(
		&item.ID, &item.PositionID, &item.PlatformID, &item.CandidateName,
		&item.Status, &item.FailReason, &item.CreatedAt, &item.UpdatedAt, &item.RequestedAt,
	); err != nil {
		return ResumeRequest{}, err
	}
	return item, nil
}

// requireResumeRequestRow 校验更新语句确实命中了记录，未命中说明记录已被岗位删除清理。
func requireResumeRequestRow(result interface{ RowsAffected() (int64, error) }, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取待索要名单更新行数失败：%w", err)
	}
	if affected == 0 {
		return fmt.Errorf("待索要名单记录不存在：%s", id)
	}
	return nil
}
