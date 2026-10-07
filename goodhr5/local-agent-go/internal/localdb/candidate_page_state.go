// 本文件将页面确认的沟通和简历事实合并到已有本地档案，不覆盖评分、简历内容或实际招呼时间。
package localdb

import (
	"context"
	"encoding/json"
	"time"
)

// ObserveCandidateState 更新稳定岗位与候选人 ID 对应的已有档案；不存在时不按姓名创建档案。
func (db *DB) ObserveCandidateState(ctx context.Context, positionID, candidateID string, contacted bool, resumeStatus string) error {
	patch := map[string]any{"platform_observed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if contacted {
		patch["contact_observed"] = true
	}
	if resumeStatus == "received" {
		patch["resume_status"] = "received"
	}
	raw, _ := json.Marshal(patch)
	_, err := db.conn.ExecContext(ctx, `UPDATE local_candidates SET ext=json_patch(COALESCE(NULLIF(ext,''),'{}'),?),
	 status=CASE WHEN ?='received' THEN 'resume_received' WHEN ? AND status NOT IN ('resume_received','greeted') THEN 'contacted' ELSE status END,
	 updated_at=? WHERE position_id=? AND id=?`, string(raw), resumeStatus, contacted, nowISO(), positionID, candidateID)
	return err
}
