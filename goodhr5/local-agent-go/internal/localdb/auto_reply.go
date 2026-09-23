// 本文件保存自动回复发送意图及状态，仅保存标识和指纹，不保存聊天或回复正文。
package localdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrAutoReplyConflict 表示消息已发送、结果不明或发送意图已被更新。
var ErrAutoReplyConflict = errors.New("回复记录已变化或不允许再次发送")

// AutoReplyRecord 保存独立于岗位生命周期的回复去重记录。
type AutoReplyRecord struct {
	ID                 string
	ProfileScope       string
	Platform           string
	ConversationID     string
	InboundFingerprint string
	PositionID         string
	RunID              string
	ContextFingerprint string
	ReplyFingerprint   string
	Status             string
	ErrorCode          string
	CreatedAt          string
	UpdatedAt          string
}

// migrateAutoReply 创建回复记录，并在程序启动时把未完成发送恢复为未知状态。
func (db *DB) migrateAutoReply() error {
	_, err := db.conn.Exec(`
CREATE TABLE IF NOT EXISTS auto_reply_records (
 -- 本次发送意图 ID，重新生成时替换以阻止迟到写入。
 id TEXT PRIMARY KEY,
 -- 实际浏览器 Profile 作用域哈希。
 profile_scope TEXT NOT NULL,
 -- 招聘平台标识。
 platform TEXT NOT NULL,
 -- 平台稳定会话标识。
 conversation_id TEXT NOT NULL,
 -- 候选人入站消息指纹。
 inbound_fingerprint TEXT NOT NULL,
 -- 本次选择的岗位 ID，不参与唯一判定。
 position_id TEXT NOT NULL,
 -- 云端许可的运行 ID，不参与唯一判定。
 run_id TEXT NOT NULL,
 -- 生成答案所依据的完整上下文指纹。
 context_fingerprint TEXT NOT NULL,
 -- 回复正文指纹，不保存正文。
 reply_fingerprint TEXT NOT NULL,
 -- 发送状态：prepared、sending、sent、obsolete、unknown。
 status TEXT NOT NULL CHECK(status IN ('prepared','sending','sent','obsolete','unknown')),
 -- 错误类别代码，不保存可能含聊天原文的错误信息。
 error_code TEXT NOT NULL DEFAULT '',
 -- 首次记录时间。
 created_at TEXT NOT NULL,
 -- 最近状态更新时间。
 updated_at TEXT NOT NULL,
 UNIQUE(profile_scope, platform, conversation_id, inbound_fingerprint)
);
UPDATE auto_reply_records SET status='unknown', error_code='interrupted', updated_at=? WHERE status='sending';
`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("初始化回复记录失败：%w", err)
	}
	return nil
}

// PrepareAutoReply 原子保存新生成的发送意图；sent、sending、unknown 不允许覆盖。
// 调用方必须已重新读取同一条仍未回复的候选人消息，不能传入旧答案。
func (db *DB) PrepareAutoReply(ctx context.Context, record AutoReplyRecord) (AutoReplyRecord, error) {
	for _, value := range []string{record.ProfileScope, record.Platform, record.ConversationID, record.InboundFingerprint, record.PositionID, record.RunID, record.ContextFingerprint, record.ReplyFingerprint} {
		if strings.TrimSpace(value) == "" {
			return AutoReplyRecord{}, fmt.Errorf("回复意图缺少身份、许可或指纹")
		}
	}
	record.ID = uuid.NewString()
	record.Status = "prepared"
	record.ErrorCode = ""
	record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.UpdatedAt = record.CreatedAt
	result, err := db.conn.ExecContext(ctx, `INSERT INTO auto_reply_records
 (id,profile_scope,platform,conversation_id,inbound_fingerprint,position_id,run_id,context_fingerprint,reply_fingerprint,status,error_code,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,'prepared','',?,?)
 ON CONFLICT(profile_scope,platform,conversation_id,inbound_fingerprint) DO UPDATE SET
 id=excluded.id, position_id=excluded.position_id, run_id=excluded.run_id,
 context_fingerprint=excluded.context_fingerprint, reply_fingerprint=excluded.reply_fingerprint,
 status='prepared', error_code='', updated_at=excluded.updated_at
 WHERE auto_reply_records.status IN ('prepared','obsolete')`,
		record.ID, record.ProfileScope, record.Platform, record.ConversationID, record.InboundFingerprint, record.PositionID, record.RunID, record.ContextFingerprint, record.ReplyFingerprint, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return AutoReplyRecord{}, fmt.Errorf("保存回复意图失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return AutoReplyRecord{}, err
	}
	if count != 1 {
		return AutoReplyRecord{}, ErrAutoReplyConflict
	}
	return record, nil
}

// FindAutoReply 根据账号、平台、稳定会话和入站指纹查询；不存在时返回 sql.ErrNoRows。
func (db *DB) FindAutoReply(ctx context.Context, key AutoReplyRecord) (AutoReplyRecord, error) {
	var record AutoReplyRecord
	err := db.conn.QueryRowContext(ctx, `SELECT id,profile_scope,platform,conversation_id,inbound_fingerprint,position_id,run_id,context_fingerprint,reply_fingerprint,status,error_code,created_at,updated_at
 FROM auto_reply_records WHERE profile_scope=? AND platform=? AND conversation_id=? AND inbound_fingerprint=?`, key.ProfileScope, key.Platform, key.ConversationID, key.InboundFingerprint).Scan(
		&record.ID, &record.ProfileScope, &record.Platform, &record.ConversationID, &record.InboundFingerprint, &record.PositionID, &record.RunID, &record.ContextFingerprint, &record.ReplyFingerprint, &record.Status, &record.ErrorCode, &record.CreatedAt, &record.UpdatedAt)
	return record, err
}

// TransitionAutoReply 使用状态比较更新保证每份意图只能进入发送一次。
// unknown 只有取得页面发送证据后才能标为 sent，不允许回到待发送。
func (db *DB) TransitionAutoReply(ctx context.Context, id, from, to, code string) error {
	allowed := from == "prepared" && (to == "sending" || to == "obsolete") ||
		from == "sending" && (to == "sent" || to == "unknown" || to == "obsolete") ||
		from == "unknown" && to == "sent"
	if !allowed {
		return fmt.Errorf("不允许的回复状态转换：%s → %s", from, to)
	}
	result, err := db.conn.ExecContext(ctx, `UPDATE auto_reply_records SET status=?,error_code=?,updated_at=? WHERE id=? AND status=?`, to, code, time.Now().UTC().Format(time.RFC3339Nano), id, from)
	if err != nil {
		return fmt.Errorf("保存回复状态失败：%w", err)
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
