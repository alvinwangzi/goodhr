// 本文件在 HRPlus 原进度回执事务内提交父消息轮换，失败时回执和规范进度一起回滚。
package localdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
)

// saveConfirmedDispatchTx 验证原状态中的轮换与实际回执一致，服务器时间使用回执规范值。
func saveConfirmedDispatchTx(ctx context.Context, tx *sql.Tx, scope string, run planmodel.Run, raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ErrPlanRequestConflict
	}
	var schema int
	var owner string
	var staged planmodel.Run
	if json.Unmarshal(fields["Schema"], &schema) != nil || json.Unmarshal(fields["OwnerScope"], &owner) != nil || json.Unmarshal(fields["Run"], &staged) != nil || schema != 1 || owner != scope {
		return ErrPlanRequestConflict
	}
	if err := staged.Validate(); err != nil {
		return err
	}
	staged.StartedAt = run.StartedAt
	staged.FinishedAt = run.FinishedAt
	if !reflect.DeepEqual(staged, run) {
		return ErrPlanRequestConflict
	}
	fields["Run"], _ = json.Marshal(run)
	confirmed, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_message_dispatch(owner_scope,run_id,run_sequence,snapshot_json) VALUES(?,?,?,?) ON CONFLICT(owner_scope,run_id) DO UPDATE SET run_sequence=excluded.run_sequence,snapshot_json=excluded.snapshot_json WHERE plan_message_dispatch.run_sequence<=excluded.run_sequence`, scope, run.ID, run.Sequence, string(confirmed))
	return err
}
