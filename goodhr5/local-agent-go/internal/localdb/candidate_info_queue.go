// 本文件扩展既有待索要名单，按稳定候选人 ID 保存三项勾选及独立发送状态，避免部分成功后重发。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// CandidateInfoRequest 保存一名候选人的勾选快照及各项独立状态，和旧版仅按姓名的简历名单隔离。
type CandidateInfoRequest struct {
	ID, PositionID, PlatformID, CandidateID, CandidateName string
	Actions                                                map[string]bool
	Results                                                map[string]string
	Synced                                                 map[string]string
}

// migrateCandidateInfoQueue 为既有名单增加身份、动作和结果字段，升级时未结束的发送保持未知而不重发。
func (db *DB) migrateCandidateInfoQueue() error {
	for _, column := range []struct{ name, definition string }{
		{"candidate_id", "TEXT NOT NULL DEFAULT ''"},   // 平台稳定候选人标识，空值属于历史仅姓名名单。
		{"info_actions", "TEXT NOT NULL DEFAULT '{}'"}, // 勾选快照：phone、wechat、resume 布尔值。
		{"info_results", "TEXT NOT NULL DEFAULT '{}'"}, // 每项状态：sending、requested、unknown、satisfied。
		{"info_synced", "TEXT NOT NULL DEFAULT '{}'"},  // 已获云端确认的各项结果状态，网络失败只重试记录同步。
	} {
		var count int
		if err := db.conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('resume_request_queue') WHERE name=?`, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.conn.Exec("ALTER TABLE resume_request_queue ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	if _, err := db.conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS resume_info_identity ON resume_request_queue(position_id,platform_id,candidate_id) WHERE candidate_id<>''`); err != nil {
		return err
	}
	for _, action := range []string{"phone", "wechat", "resume"} {
		path := "$." + action
		if _, err := db.conn.Exec(`UPDATE resume_request_queue SET info_results=json_set(info_results,?,'unknown') WHERE candidate_id<>'' AND json_extract(info_results,?)='sending'`, path, path); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueCandidateInfoRequest 保存勾选快照，已发送或结果未知的状态在再次入队时保持，不用姓名去重。
func (db *DB) EnqueueCandidateInfoRequest(positionID, platformID, candidateID, name string, actions map[string]bool) (CandidateInfoRequest, error) {
	if strings.TrimSpace(positionID) == "" || strings.TrimSpace(candidateID) == "" || strings.TrimSpace(name) == "" {
		return CandidateInfoRequest{}, fmt.Errorf("写入待索要名单失败：缺少岗位、稳定候选人 ID 或姓名")
	}
	selected := map[string]bool{}
	for _, action := range []string{"phone", "wechat", "resume"} {
		if actions[action] {
			selected[action] = true
		}
	}
	if len(selected) == 0 {
		return CandidateInfoRequest{}, fmt.Errorf("没有勾选索要项")
	}
	raw, _ := json.Marshal(selected)
	_, err := db.conn.Exec(`INSERT INTO resume_request_queue(id,position_id,platform_id,candidate_id,candidate_name,status,created_at,updated_at,info_actions,info_results)
	 VALUES(?,?,?,?,?,'pending',?,?,?,'{}')
	 ON CONFLICT(position_id,platform_id,candidate_id) WHERE candidate_id<>'' DO UPDATE SET
	 info_actions=json_patch(resume_request_queue.info_actions,excluded.info_actions), status='pending', updated_at=excluded.updated_at`, uuid.NewString(), positionID, platformID, candidateID, strings.TrimSpace(name), nowISO(), nowISO(), string(raw))
	if err != nil {
		return CandidateInfoRequest{}, err
	}
	items, err := db.ListCandidateInfoRequests(positionID)
	if err != nil {
		return CandidateInfoRequest{}, err
	}
	for _, item := range items {
		if item.PlatformID == platformID && item.CandidateID == candidateID {
			return item, nil
		}
	}
	return CandidateInfoRequest{}, sql.ErrNoRows
}

// ListCandidateInfoRequests 读取待检查和未确认的三项意图，不与历史仅按姓名的名单混用。
func (db *DB) ListCandidateInfoRequests(positionID string) ([]CandidateInfoRequest, error) {
	rows, err := db.conn.Query(`SELECT id,position_id,platform_id,candidate_id,candidate_name,info_actions,info_results,info_synced FROM resume_request_queue WHERE position_id=? AND candidate_id<>'' AND status='pending' ORDER BY created_at`, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CandidateInfoRequest{}
	for rows.Next() {
		var item CandidateInfoRequest
		var actions, results, synced string
		if err := rows.Scan(&item.ID, &item.PositionID, &item.PlatformID, &item.CandidateID, &item.CandidateName, &actions, &results, &synced); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(actions), &item.Actions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(results), &item.Results); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(synced), &item.Synced); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// MarkCandidateInfoSynced 只确认当前修订，结果从 unknown 变为 requested 后仍需重新同步。
func (db *DB) MarkCandidateInfoSynced(id, action, state string) error {
	path := "$." + action
	_, err := db.conn.Exec(`UPDATE resume_request_queue SET info_synced=json_set(info_synced,?,?) WHERE id=? AND json_extract(info_results,?)=?`, path, state, id, path, state)
	return err
}

// ClaimCandidateInfoAction 原子登记发送边界，只有首次未处理动作可以进入 sending，未知或成功不再发。
func (db *DB) ClaimCandidateInfoAction(id, action string) error {
	if action != "phone" && action != "wechat" && action != "resume" {
		return fmt.Errorf("不支持的索要项")
	}
	path := "$." + action
	result, err := db.conn.Exec(`UPDATE resume_request_queue SET info_results=json_set(info_results,?,'sending'),updated_at=? WHERE id=? AND json_extract(info_actions,?)=1 AND COALESCE(json_extract(info_results,?),'')=''`, path, nowISO(), id, path, path)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("索要动作已处理或不能再次发送")
	}
	return nil
}

// SaveCandidateInfoResult 保存每项结果，不允许把未知或成功改回未发送状态。
func (db *DB) SaveCandidateInfoResult(id, action, state string) error {
	if action != "phone" && action != "wechat" && action != "resume" {
		return fmt.Errorf("不支持的索要项")
	}
	if state != "requested" && state != "unknown" && state != "satisfied" {
		return fmt.Errorf("不支持的索要结果")
	}
	tx, err := db.conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveInfoResultTx(context.Background(), tx, id, action, state); err != nil {
		return err
	}
	return tx.Commit()
}
