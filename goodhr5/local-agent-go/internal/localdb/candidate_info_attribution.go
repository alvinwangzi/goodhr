// 本文件为 HRPlus 实际索要动作保留原任务归属，重复岗位或恢复检查不改记后续执行项。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// migrateInfoAttribution 建立实际发送归属表，字段均有中文说明。
func (db *DB) migrateInfoAttribution() error {
	_, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS candidate_info_attributions (
 -- 原候选人索要记录编号。
 request_id TEXT NOT NULL,
 -- 实际索要类型。
 action TEXT NOT NULL CHECK(action IN ('resume','phone','wechat')),
 -- 原本地检查点编号。
 local_run_id TEXT NOT NULL,
 -- 原云端岗位任务编号。
 cloud_run_id TEXT NOT NULL,
 -- 原账号作用域，历史普通单岗位可能为空。
 owner_scope TEXT NOT NULL,
 -- 原父计划运行，普通单岗位为空。
 plan_run_id TEXT NOT NULL,
 -- 原独立执行项，普通单岗位为空。
 item_run_id TEXT NOT NULL,
 -- 发送中、已确认或结果待核对。
 state TEXT NOT NULL CHECK(state IN ('sending','requested','unknown','satisfied')),
 -- 首次实际领取时间。
 created_at TEXT NOT NULL,
 -- 最近结果核对时间。
 updated_at TEXT NOT NULL,
 PRIMARY KEY(request_id,action)
);`)
	return err
}

// ClaimCandidateInfoForRun 在同一事务领取发送并保存原任务，后续岗位不能抢走归属。
func (db *DB) ClaimCandidateInfoForRun(ctx context.Context, id, action, runID string) error {
	if action != "resume" && action != "phone" && action != "wechat" {
		return fmt.Errorf("不支持的索要项")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw, positionID, platformID string
	if err := tx.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE run_id=?`, runID).Scan(&raw); err != nil {
		return err
	}
	var cp ActionCheckpoint
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return err
	}
	if cp.RunID != runID || cp.CloudRunID == "" {
		return ErrPlanRequestConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT position_id,platform_id FROM resume_request_queue WHERE id=?`, id).Scan(&positionID, &platformID); err != nil {
		return err
	}
	if positionID != cp.PositionID || platformID != cp.Platform {
		return ErrPlanRequestConflict
	}
	path := "$." + action
	result, err := tx.ExecContext(ctx, `UPDATE resume_request_queue SET info_results=json_set(info_results,?,'sending'),updated_at=? WHERE id=? AND json_extract(info_actions,?)=1 AND COALESCE(json_extract(info_results,?),'')=''`, path, nowISO(), id, path, path)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("索要动作已处理或不能再次发送")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO candidate_info_attributions(request_id,action,local_run_id,cloud_run_id,owner_scope,plan_run_id,item_run_id,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'sending',?,?)`, id, action, runID, cp.CloudRunID, cp.OwnerScope, cp.PlanRunID, cp.ItemRunID, nowISO(), nowISO())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// InfoAttribution 保存原动作归属，不包含候选人联系方式或消息正文。
type InfoAttribution struct{ RequestID, Action, LocalRunID, CloudRunID, OwnerScope, PlanRunID, ItemRunID, State string }

// InfoAttributions 读取原执行项实际发起动作，发送中作为待核对，不能算成功。
func (db *DB) InfoAttributions(ctx context.Context, runID string) ([]InfoAttribution, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT request_id,action,local_run_id,cloud_run_id,owner_scope,plan_run_id,item_run_id,state FROM candidate_info_attributions WHERE local_run_id=? ORDER BY created_at,request_id,action`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InfoAttribution{}
	for rows.Next() {
		var item InfoAttribution
		if err := rows.Scan(&item.RequestID, &item.Action, &item.LocalRunID, &item.CloudRunID, &item.OwnerScope, &item.PlanRunID, &item.ItemRunID, &item.State); err != nil {
			return nil, err
		}
		if item.State == "sending" {
			item.State = "unknown"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// saveInfoResultTx 一起保存名单和原任务结果，恢复检查不改变发送来源。
func saveInfoResultTx(ctx context.Context, tx *sql.Tx, id, action, state string) error {
	path := "$." + action
	result, err := tx.ExecContext(ctx, `UPDATE resume_request_queue SET info_results=json_set(info_results,?,?),updated_at=? WHERE id=? AND COALESCE(json_extract(info_results,?),'') NOT IN ('requested','satisfied')`, path, state, nowISO(), id, path)
	if err != nil {
		return err
	}
	if err := requireResumeRequestRow(result, id); err != nil {
		return err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT info_results FROM resume_request_queue WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	var facts map[string]string
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE candidate_info_attributions SET state=?,updated_at=? WHERE request_id=? AND action=? AND state IN ('sending','unknown')`, facts[action], nowISO(), id, action)
	return err
}
