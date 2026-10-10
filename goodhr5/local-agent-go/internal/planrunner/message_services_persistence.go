// 本文件把 HRPlus 原父计划的消息轮换接入真实 SQLite，读取不恢复页面授权。
package planrunner

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/planmodel"
)

// MessageStateRuntime 是真实适配器可选的安全轮换持久化契约，不复制平台消息执行。
type MessageStateRuntime interface {
	LoadMessageState(context.Context, planmodel.Run) ([]byte, error)
	SaveMessageState(context.Context, planmodel.Run, []byte) error
}

// LoadMessageState 只读原账号和当天运行；不存在记录时让原服务重新核对当前工作。
func (r *M1ExecutionRuntime) LoadMessageState(ctx context.Context, run planmodel.Run) ([]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if run.ID != r.held.Permit.Run.ID {
		return nil, ErrPlanAuthority
	}
	raw, err := r.coordinator.db.PlanMessageDispatch(ctx, r.held.scope, run.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

// SaveMessageState 只有原云端进度已经确认才保存轮换，失败时主循环不进入下一步骤。
func (r *M1ExecutionRuntime) SaveMessageState(ctx context.Context, run planmodel.Run, raw []byte) error {
	if err := r.check(); err != nil {
		return err
	}
	if run.ID != r.held.Permit.Run.ID {
		return ErrPlanAuthority
	}
	return r.coordinator.db.SavePlanMessageDispatch(ctx, r.held.scope, run.ID, run.Sequence, raw)
}
