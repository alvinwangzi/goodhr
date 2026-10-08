// 本文件查询 HRPlus 跨单岗位运行的未确认发送，重新开始不能清空候选人发送保护。
package localdb

import "context"

// UnresolvedActionCandidate 保存需要页面核对的原运行归属，不用新运行覆盖历史。
type UnresolvedActionCandidate struct{ RunID, PositionID string }

// UnresolvedActionCandidates 按真实候选人、账号作用域和平台查询所有未确认运行。
func (db *DB) UnresolvedActionCandidates(ctx context.Context, scope, platform, candidateID string) ([]UnresolvedActionCandidate, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT c.run_id,c.position_id FROM action_candidates c JOIN action_runs r ON r.run_id=c.run_id WHERE r.profile_scope=? AND r.platform=? AND c.recommendation_id=? AND c.status IN ('unknown','processing') AND (c.status='unknown' OR c.reason<>'candidate_processing')`, scope, platform, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UnresolvedActionCandidate{}
	for rows.Next() {
		var item UnresolvedActionCandidate
		if err := rows.Scan(&item.RunID, &item.PositionID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// MarkActionGreetingSending 在任何招呼页面操作开始前持久化发送边界，读简历阶段与可能已发送阶段分别恢复。
func (db *DB) MarkActionGreetingSending(ctx context.Context, runID, positionID, candidateID string) error {
	result, err := db.conn.ExecContext(ctx, `UPDATE action_candidates SET reason='greet_sending' WHERE run_id=? AND position_id=? AND recommendation_id=? AND status IN ('processing','retry_pending')`, runID, positionID, candidateID)
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
