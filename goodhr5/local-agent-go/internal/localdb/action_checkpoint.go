// 本文件保存 HRPlus 单岗位运行检查点、候选人处理结果、身份映射与独立复打到期时间。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrIdentityConflict 表示同一账号作用域内已有不同的候选人身份映射。
var ErrIdentityConflict = errors.New("候选人身份映射冲突，需要核对")

// ActionCheckpoint 保存当前单岗位进度，不包含登录凭证或页面对象。
type ActionCheckpoint struct {
	TaskType         string           `json:"task_type"`                // 本次勾选的单岗位动作组合。
	PrioritizeReply  bool             `json:"prioritize_reply"`         // 本次固定的优先回复开关。
	LastMessageCheck time.Time        `json:"last_message_check"`       // 最近实际完成的消息检查时间。
	ReplyStats       map[string]int   `json:"reply_stats,omitempty"`    // 本次回复、附件、失败与未知的分别统计。
	ReGreetStats     map[string]int   `json:"re_greet_stats,omitempty"` // 本次复打动作的分别统计。
	RunID            string           `json:"run_id"`
	PositionID       string           `json:"position_id"`
	ProfileScope     string           `json:"profile_scope"`
	Platform         string           `json:"platform"`
	CloudRunID       string           `json:"cloud_run_id"`
	PositionSnapshot map[string]any   `json:"position_snapshot"`
	Queue            []map[string]any `json:"queue"`
	Anchors          []string         `json:"anchors"`
	FilterHash       string           `json:"filter_hash"`
	CurrentAction    string           `json:"current_action"`
	Greeted          int              `json:"greeted"`
	Replied          int              `json:"replied"`
	ReGreeted        int              `json:"re_greeted"`
	NextMessageCheck time.Time        `json:"next_message_check"`
}

// CandidateIdentity 分开保存两个入口的标识，验证来源必须来自实际直接跳转证据。
type CandidateIdentity struct {
	ProfileScope, Platform, RecommendationID, ConversationID, Source, Status string
}

// migrateActionCheckpoint 创建 M1 独立数据，不依赖多岗位计划表。
func (db *DB) migrateActionCheckpoint() error {
	_, err := db.conn.Exec(`
CREATE TABLE IF NOT EXISTS action_runs (
 -- 本地单岗位运行编号，每次明确开始独立生成。
 run_id TEXT PRIMARY KEY,
 -- 本次岗位编号。
 position_id TEXT NOT NULL,
 -- 浏览器账号作用域摘要。
 profile_scope TEXT NOT NULL,
 -- 招聘平台标识。
 platform TEXT NOT NULL,
 -- 检查点 JSON，不含凭证和 DOM 引用。
 checkpoint TEXT NOT NULL,
 -- 原子递增的开始顺序，不受系统时间调整或旧进度补传影响。
 start_seq INTEGER NOT NULL UNIQUE,
 -- 首次创建时间，后续进度更新不改变运行排序。
 created_at TEXT NOT NULL,
 -- 最近持久化时间。
 updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS action_candidates (
 -- 本地运行编号。
 run_id TEXT NOT NULL REFERENCES action_runs(run_id),
 -- 岗位编号。
 position_id TEXT NOT NULL,
 -- 页面完整推荐候选人标识。
 recommendation_id TEXT NOT NULL,
 -- 处理状态，只有 completed 与 skipped 算完成。
 status TEXT NOT NULL CHECK(status IN ('completed','skipped','processing','retry_pending','unknown')),
 -- 明确结果或待核对原因代码。
 reason TEXT NOT NULL DEFAULT '',
 -- 最近更新时间。
 updated_at TEXT NOT NULL,
 PRIMARY KEY(run_id,position_id,recommendation_id)
);
CREATE TABLE IF NOT EXISTS candidate_identities (
 -- 浏览器账号作用域摘要。
 profile_scope TEXT NOT NULL,
 -- 平台标识。
 platform TEXT NOT NULL,
 -- 推荐页完整标识。
 recommendation_id TEXT NOT NULL,
 -- 聊天页完整标识，未知时为空。
 conversation_id TEXT NOT NULL DEFAULT '',
 -- verified、unresolved 或 conflict，历史记录保留 legacy 来源。
 status TEXT NOT NULL CHECK(status IN ('verified','unresolved','conflict')),
 -- 实际映射的验证来源，不能用姓名推断。
 source TEXT NOT NULL,
 -- 最近核对时间。
 updated_at TEXT NOT NULL,
 PRIMARY KEY(profile_scope,platform,recommendation_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS candidate_verified_conversation ON candidate_identities(profile_scope,platform,conversation_id) WHERE status='verified';
CREATE TABLE IF NOT EXISTS re_greet_schedules (
 -- 浏览器账号作用域摘要。
 profile_scope TEXT NOT NULL,
 -- 平台标识。
 platform TEXT NOT NULL,
 -- 候选人完整平台标识。
 candidate_id TEXT NOT NULL,
 -- 已确认联系事实的基准摘要。
 contact_basis TEXT NOT NULL,
 -- 在同一基准下只生成一次的到期时间。
 due_at TEXT NOT NULL,
 PRIMARY KEY(profile_scope,platform,candidate_id)
);`)
	if err != nil {
		return err
	}
	rows, err := db.conn.Query(`PRAGMA table_info(action_runs)`)
	if err != nil {
		return err
	}
	found, sequenceFound := false, false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "created_at" {
			found = true
		}
		if name == "start_seq" {
			sequenceFound = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		if _, err = db.conn.Exec(`ALTER TABLE action_runs ADD COLUMN created_at TEXT NOT NULL DEFAULT ''; UPDATE action_runs SET created_at=updated_at WHERE created_at='';`); err != nil {
			return err
		}
	}
	if !sequenceFound {
		if _, err = db.conn.Exec(`ALTER TABLE action_runs ADD COLUMN start_seq INTEGER NOT NULL DEFAULT 0; UPDATE action_runs SET start_seq=rowid; CREATE UNIQUE INDEX IF NOT EXISTS action_runs_sequence ON action_runs(start_seq);`); err != nil {
			return err
		}
	}
	return nil
}

// CreateActionRun 保存新的单岗位运行，调用方仅提供不含 Token 的岗位快照。
func (db *DB) CreateActionRun(ctx context.Context, checkpoint ActionCheckpoint) (ActionCheckpoint, error) {
	if checkpoint.PositionID == "" || checkpoint.ProfileScope == "" || checkpoint.Platform == "" {
		return ActionCheckpoint{}, fmt.Errorf("单岗位运行缺少岗位或账号作用域")
	}
	checkpoint.RunID = uuid.NewString()
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO action_runs(run_id,position_id,profile_scope,platform,checkpoint,start_seq,created_at,updated_at) SELECT ?,?,?,?,?,COALESCE(MAX(start_seq),0)+1,?,? FROM action_runs`, checkpoint.RunID, checkpoint.PositionID, checkpoint.ProfileScope, checkpoint.Platform, string(raw), now, now)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	return checkpoint, tx.Commit()
}

// LoadActionCheckpoint 读取持久化检查点，重新打开数据库后仍可核对进度。
func (db *DB) LoadActionCheckpoint(ctx context.Context, runID string) (ActionCheckpoint, error) {
	var raw string
	var checkpoint ActionCheckpoint
	err := db.conn.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE run_id=?`, runID).Scan(&raw)
	if err != nil {
		return checkpoint, err
	}
	err = json.Unmarshal([]byte(raw), &checkpoint)
	return checkpoint, err
}

// LatestActionCheckpoint 按原子开始顺序读取最近一轮，时钟调整和旧补传不能取代新运行。
func (db *DB) LatestActionCheckpoint(ctx context.Context, positionID string) (ActionCheckpoint, error) {
	var raw string
	var checkpoint ActionCheckpoint
	err := db.conn.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE position_id=? ORDER BY start_seq DESC LIMIT 1`, positionID).Scan(&raw)
	if err != nil {
		return checkpoint, err
	}
	err = json.Unmarshal([]byte(raw), &checkpoint)
	return checkpoint, err
}

// SaveActionCheckpoint 保存动作切换前的纯数据检查点，账号和岗位身份不允许改变。
func (db *DB) SaveActionCheckpoint(ctx context.Context, checkpoint ActionCheckpoint) error {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	result, err := db.conn.ExecContext(ctx, `UPDATE action_runs SET checkpoint=?,updated_at=? WHERE run_id=? AND position_id=? AND profile_scope=? AND platform=?`, string(raw), time.Now().UTC().Format(time.RFC3339Nano), checkpoint.RunID, checkpoint.PositionID, checkpoint.ProfileScope, checkpoint.Platform)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("单岗位检查点身份不匹配")
	}
	return nil
}

// ActionCompletedIDs 只返回本次已完成或明确跳过的人，未知和待重试不能混入完成集合。
func (db *DB) ActionCompletedIDs(ctx context.Context, runID, positionID string) ([]string, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT recommendation_id FROM action_candidates WHERE run_id=? AND position_id=? AND status IN ('completed','skipped') ORDER BY recommendation_id`, runID, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ActionCandidateStates 返回本次候选人的各类状态，恢复时区分已完成、待重试和未知。
func (db *DB) ActionCandidateStates(ctx context.Context, runID, positionID string) (map[string]string, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT recommendation_id,status FROM action_candidates WHERE run_id=? AND position_id=?`, runID, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]string{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		states[id] = status
	}
	return states, rows.Err()
}

// SaveActionCandidate 原子保存处理结果及检查点，结果不明不得进入完成锚点。
func (db *DB) SaveActionCandidate(ctx context.Context, checkpoint ActionCheckpoint, candidateID, status, reason string) error {
	if strings.TrimSpace(candidateID) == "" {
		return fmt.Errorf("候选人真实标识为空")
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE action_runs SET checkpoint=?,updated_at=? WHERE run_id=? AND position_id=? AND profile_scope=? AND platform=?`, string(raw), now, checkpoint.RunID, checkpoint.PositionID, checkpoint.ProfileScope, checkpoint.Platform)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("单岗位运行检查点不匹配")
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO action_candidates(run_id,position_id,recommendation_id,status,reason,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,position_id,recommendation_id) DO UPDATE SET status=excluded.status,reason=excluded.reason,updated_at=excluded.updated_at WHERE action_candidates.status NOT IN ('completed','skipped','unknown') OR action_candidates.status=excluded.status`, checkpoint.RunID, checkpoint.PositionID, candidateID, status, reason, now)
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("候选人已完成或结果不明，不能改变状态")
	}
	return tx.Commit()
}

// ActionCandidateStatus 按运行和真实 ID 查询状态，不把数组位置或姓名当身份。
func (db *DB) ActionCandidateStatus(ctx context.Context, runID, positionID, candidateID string) (string, error) {
	var status string
	err := db.conn.QueryRowContext(ctx, `SELECT status FROM action_candidates WHERE run_id=? AND position_id=? AND recommendation_id=?`, runID, positionID, candidateID).Scan(&status)
	return status, err
}

// SaveCandidateIdentity 保存明确验证的映射；冲突会持久标记，不能悄悄覆盖原对应关系。
func (db *DB) SaveCandidateIdentity(ctx context.Context, identity CandidateIdentity) error {
	if identity.ProfileScope == "" || identity.Platform == "" || identity.RecommendationID == "" || identity.Source == "" || (identity.Status == "verified" && identity.ConversationID == "") {
		return fmt.Errorf("候选人映射缺少标识或验证来源")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing, existingStatus string
	err = tx.QueryRowContext(ctx, `SELECT conversation_id,status FROM candidate_identities WHERE profile_scope=? AND platform=? AND recommendation_id=?`, identity.ProfileScope, identity.Platform, identity.RecommendationID).Scan(&existing, &existingStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	conflict := existingStatus == "conflict" || (existingStatus == "verified" && identity.Status == "verified" && existing != identity.ConversationID)
	if identity.Status == "verified" {
		var other int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM candidate_identities WHERE profile_scope=? AND platform=? AND conversation_id=? AND recommendation_id<>? AND status='verified'`, identity.ProfileScope, identity.Platform, identity.ConversationID, identity.RecommendationID).Scan(&other); err != nil {
			return err
		}
		if other > 0 {
			conflict = true
			if _, err := tx.ExecContext(ctx, `UPDATE candidate_identities SET status='conflict' WHERE profile_scope=? AND platform=? AND conversation_id=?`, identity.ProfileScope, identity.Platform, identity.ConversationID); err != nil {
				return err
			}
		}
	}
	if conflict {
		identity.Status = "conflict"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO candidate_identities(profile_scope,platform,recommendation_id,conversation_id,status,source,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(profile_scope,platform,recommendation_id) DO UPDATE SET conversation_id=CASE WHEN candidate_identities.status='verified' THEN candidate_identities.conversation_id ELSE excluded.conversation_id END,status=CASE WHEN candidate_identities.status='verified' AND excluded.status='unresolved' THEN 'verified' ELSE excluded.status END,source=CASE WHEN candidate_identities.status='verified' AND excluded.status='unresolved' THEN candidate_identities.source ELSE excluded.source END,updated_at=excluded.updated_at`, identity.ProfileScope, identity.Platform, identity.RecommendationID, identity.ConversationID, identity.Status, identity.Source, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if conflict {
		return ErrIdentityConflict
	}
	return nil
}

// CandidateIdentityFor 查找候选人映射，调用方只可使用 verified 身份发送。
func (db *DB) CandidateIdentityFor(ctx context.Context, scope, platform, recommendationID string) (CandidateIdentity, error) {
	result := CandidateIdentity{ProfileScope: scope, Platform: platform, RecommendationID: recommendationID}
	err := db.conn.QueryRowContext(ctx, `SELECT conversation_id,status,source FROM candidate_identities WHERE profile_scope=? AND platform=? AND recommendation_id=?`, scope, platform, recommendationID).Scan(&result.ConversationID, &result.Status, &result.Source)
	return result, err
}

// EnsureReGreetDue 在联系基准改变时才保存新抽样，同一基准重试和重启返回原时间。
func (db *DB) EnsureReGreetDue(ctx context.Context, scope, platform, candidateID, basis string, sampled time.Time) (time.Time, error) {
	if scope == "" || platform == "" || candidateID == "" || basis == "" || sampled.IsZero() {
		return time.Time{}, fmt.Errorf("复打到期安排缺少身份或联系基准")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO re_greet_schedules(profile_scope,platform,candidate_id,contact_basis,due_at) VALUES(?,?,?,?,?) ON CONFLICT(profile_scope,platform,candidate_id) DO UPDATE SET contact_basis=excluded.contact_basis,due_at=excluded.due_at WHERE re_greet_schedules.contact_basis<>excluded.contact_basis`, scope, platform, candidateID, basis, sampled.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return time.Time{}, err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT due_at FROM re_greet_schedules WHERE profile_scope=? AND platform=? AND candidate_id=?`, scope, platform, candidateID).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	due, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return due, tx.Commit()
}
