// 本文件持久化简历库待同步进度，网络失败可重试，但不会重新执行招聘页面动作。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// ResumeTrackingRecord 保存一名候选人在一个岗位的进度快照；Scope 隔离云端、平台和浏览器账号。
type ResumeTrackingRecord struct {
	Scope string
	PositionID string
	CandidateID string
	Payload map[string]any
	Revision string
}

// migrateResumeTracking 创建独立进度表，不在通用设置接口暴露候选人信息。
func (db *DB) migrateResumeTracking() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS resume_tracking (
	 -- 云端地址、平台和浏览器账号组成的作用域摘要。
	 scope TEXT NOT NULL,
	 -- 云端岗位标识。
	 position_id TEXT NOT NULL,
	 -- 当前来源下稳定的候选人标识。
	 candidate_id TEXT NOT NULL,
	 -- 仅包含身份摘要、评分、实际进度和失败原因的 JSON。
	 payload TEXT NOT NULL,
	 -- 进度修订时间，防止旧同步响应误清除新状态。
	 revision TEXT NOT NULL,
	 -- 是否已获云端明确确认。
	 synced INTEGER NOT NULL DEFAULT 0,
	 PRIMARY KEY(scope,position_id,candidate_id)
	);`)
	return err
}

// QueueResumeTracking 合并进度并持久化，低阶段报告不能使已有下载等事实回退。
func (db *DB) QueueResumeTracking(ctx context.Context, scope, positionID string, payload map[string]any) (ResumeTrackingRecord, error) {
	id, _ := payload["id"].(string)
	state, _ := payload["state"].(string)
	rank := map[string]int{"pending":1,"requested":2,"received":3,"downloaded":4}
	if scope == "" || positionID == "" || id == "" || rank[state] == 0 { return ResumeTrackingRecord{}, fmt.Errorf("简历跟踪身份或进度不完整") }
	tx, err := db.conn.BeginTx(ctx,nil)
	if err != nil { return ResumeTrackingRecord{}, err }
	defer tx.Rollback()
	var raw, revision string
	err = tx.QueryRowContext(ctx, `SELECT payload,revision FROM resume_tracking WHERE scope=? AND position_id=? AND candidate_id=?`,scope,positionID,id).Scan(&raw,&revision)
	if err != nil && !errors.Is(err,sql.ErrNoRows) { return ResumeTrackingRecord{}, err }
	previous := map[string]any{}
	if raw != "" { if err := json.Unmarshal([]byte(raw),&previous); err != nil { return ResumeTrackingRecord{},err } }
	// JSON 往返统一数字类型，同时避免修改调用方对象。
	encoded, err := json.Marshal(payload)
	if err != nil { return ResumeTrackingRecord{},err }
	next := map[string]any{}
	if err := json.Unmarshal(encoded,&next); err != nil { return ResumeTrackingRecord{},err }
	oldState, _ := previous["state"].(string)
	if rank[state] < rank[oldState] { next = previous } else {
		if next["score"] == nil && previous["score"] != nil { next["score"],next["reason"] = previous["score"],previous["reason"] }
		next["updated_at"] = previous["updated_at"]
	}
	if !reflect.DeepEqual(next,previous) {
		now := time.Now().UTC()
		if old, err := time.Parse(time.RFC3339Nano,revision); err == nil && !now.After(old) { now=old.Add(time.Nanosecond) }
		revision=now.Format(time.RFC3339Nano)
		next["updated_at"] = revision
		encoded, err = json.Marshal(next)
		if err != nil { return ResumeTrackingRecord{},err }
		_,err=tx.ExecContext(ctx, `INSERT INTO resume_tracking(scope,position_id,candidate_id,payload,revision,synced) VALUES(?,?,?,?,?,0)
		ON CONFLICT(scope,position_id,candidate_id) DO UPDATE SET payload=excluded.payload,revision=excluded.revision,synced=0`,scope,positionID,id,string(encoded),revision)
		if err != nil { return ResumeTrackingRecord{},err }
	}
	if err:=tx.Commit(); err != nil { return ResumeTrackingRecord{},err }
	return ResumeTrackingRecord{Scope:scope,PositionID:positionID,CandidateID:id,Payload:next,Revision:revision},nil
}

// PendingResumeTracking 只取当前登录作用域和岗位待补报的记录，防止切换账号时串数据。
func (db *DB) PendingResumeTracking(ctx context.Context, scope, positionID string) ([]ResumeTrackingRecord,error) {
	rows,err:=db.conn.QueryContext(ctx,`SELECT candidate_id,payload,revision FROM resume_tracking WHERE scope=? AND position_id=? AND synced=0 ORDER BY revision LIMIT 100`,scope,positionID)
	if err != nil { return nil,err }
	defer rows.Close()
	result:=[]ResumeTrackingRecord{}
	for rows.Next() {
		item:=ResumeTrackingRecord{Scope:scope,PositionID:positionID}
		var raw string
		if err:=rows.Scan(&item.CandidateID,&raw,&item.Revision); err != nil { return nil,err }
		if err:=json.Unmarshal([]byte(raw),&item.Payload); err != nil { return nil,err }
		result=append(result,item)
	}
	return result,rows.Err()
}

// MarkResumeTrackingSynced 仅确认已经发送的修订，较新的本地进度继续保留待同步。
func (db *DB) MarkResumeTrackingSynced(ctx context.Context, item ResumeTrackingRecord) error {
	_,err:=db.conn.ExecContext(ctx,`UPDATE resume_tracking SET synced=1 WHERE scope=? AND position_id=? AND candidate_id=? AND revision=?`,item.Scope,item.PositionID,item.CandidateID,item.Revision)
	return err
}
