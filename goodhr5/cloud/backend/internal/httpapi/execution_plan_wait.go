// 本文件保存 HRPlus 原窗口排队事实，原编号幂等且不领取执行权或启动任务。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// ExecutionPlanWait 保留指定电脑持久队列的原时间，不接受新的上报时间替换。
type ExecutionPlanWait struct {
	PlanID        string     `json:"plan_id"`
	RequestID     string     `json:"request_id"`
	ActivationID  string     `json:"activation_id"`
	ConfigVersion int64      `json:"config_version"`
	MachineID     string     `json:"machine_id"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	QueuedAt      *time.Time `json:"queued_at,omitempty"`
}

// verifyPlanWait 核对原批次和名义开始点，只验证原排队事实，不授予页面操作许可。
func verifyPlanWait(plan ExecutionPlan, wait ExecutionPlanWait) error {
	if wait.QueuedAt != nil && (wait.QueuedAt.IsZero() || wait.QueuedAt.Nanosecond()%1000 != 0) {
		return ErrExecutionPlanRequest
	}
	if !executionPlanUUID.MatchString(wait.RequestID) || wait.PlanID != plan.ID || wait.ActivationID != plan.ActivationID || wait.ConfigVersion != plan.Version || wait.MachineID != plan.MachineID || plan.State != "enabled" || plan.StopRequested || wait.TriggeredAt.IsZero() {
		return ErrExecutionPlanRequest
	}
	loc, err := time.LoadLocation(plan.Config.Schedule.Timezone)
	if err != nil {
		return err
	}
	at := wait.TriggeredAt.In(loc)
	if at.Second() != 0 || at.Nanosecond() != 0 || !runtimeDateAllowed(plan.Config.Schedule, at) {
		return ErrExecutionPlanRequest
	}
	for _, window := range plan.Config.Schedule.Windows {
		if window.StartMinute == at.Hour()*60+at.Minute() {
			return nil
		}
	}
	return ErrExecutionPlanRequest
}

// clonePlanWait 隔离可空时间指针，读取与重试都不能修改原入队事实。
func clonePlanWait(wait ExecutionPlanWait) ExecutionPlanWait {
	if wait.QueuedAt != nil {
		copy := wait.QueuedAt.UTC()
		wait.QueuedAt = &copy
	}
	return wait
}

// samePlanWait 不把同一瞬间的不同时区序列化当作另一份原请求。
func samePlanWait(a, b ExecutionPlanWait) bool {
	return a.PlanID == b.PlanID && a.RequestID == b.RequestID && a.ActivationID == b.ActivationID && a.ConfigVersion == b.ConfigVersion && a.MachineID == b.MachineID && a.TriggeredAt.Equal(b.TriggeredAt) && (a.QueuedAt == nil && b.QueuedAt == nil || a.QueuedAt != nil && b.QueuedAt != nil && a.QueuedAt.Equal(*b.QueuedAt))
}

// RecordWait 在原计划锁下幂等保存，重复原内容不更新事实或创建运行。
func (s *MemoryExecutionPlanStore) RecordWait(ctx context.Context, tenant, email string, input ExecutionPlanWait) (ExecutionPlanWait, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return input, err
	}
	plan, ok := s.plans[input.PlanID]
	if !ok || s.deleted[input.PlanID] || plan.TenantID != tenant || plan.UserEmail != email {
		return input, ErrNotFound
	}
	if err := verifyPlanWait(plan, input); err != nil {
		return input, err
	}
	key := input.PlanID + "/" + input.RequestID
	if old, exists := s.waits[key]; exists {
		if !samePlanWait(old, input) {
			return input, ErrExecutionPlanRequest
		}
		return clonePlanWait(old), nil
	}
	if s.waits == nil {
		s.waits = map[string]ExecutionPlanWait{}
	}
	input.TriggeredAt = input.TriggeredAt.UTC()
	s.waits[key] = clonePlanWait(input)
	return clonePlanWait(input), nil
}

// RecordWait 使用原计划行锁核对停止/编辑竞态，旧编号不能绑定另一窗口或电脑。
func (s *PostgresExecutionPlanStore) RecordWait(ctx context.Context, tenant, email string, input ExecutionPlanWait) (ExecutionPlanWait, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer tx.Rollback()
	plan, err := scanExecutionPlan(tx.QueryRowContext(ctx, `SELECT `+executionPlanColumns+` FROM execution_plans WHERE id=$1 AND COALESCE(tenant_id::text,'')=$2 AND user_email=$3 AND deleted_at IS NULL FOR UPDATE`, input.PlanID, tenant, email))
	if err != nil {
		return input, err
	}
	if err = verifyPlanWait(plan, input); err != nil {
		return input, err
	}
	var old ExecutionPlanWait
	err = tx.QueryRowContext(ctx, `SELECT plan_id::text,request_id::text,activation_id::text,config_version,machine_id,triggered_at,queued_at FROM execution_plan_waits WHERE plan_id=$1 AND request_id=$2`, input.PlanID, input.RequestID).Scan(&old.PlanID, &old.RequestID, &old.ActivationID, &old.ConfigVersion, &old.MachineID, &old.TriggeredAt, &old.QueuedAt)
	if err == nil {
		if !samePlanWait(old, input) {
			return input, ErrExecutionPlanRequest
		}
		return old, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return input, err
	}
	input.TriggeredAt = input.TriggeredAt.UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_plan_waits(plan_id,request_id,activation_id,config_version,machine_id,triggered_at,queued_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, input.PlanID, input.RequestID, input.ActivationID, input.ConfigVersion, input.MachineID, input.TriggeredAt, input.QueuedAt)
	if err != nil {
		return input, err
	}
	return input, tx.Commit()
}

// recordWait 只允许已绑定的指定执行电脑提交原排队记录，排队回执不返回执行许可。
func (s *ExecutionPlanService) recordWait(w http.ResponseWriter, r *http.Request, tenant, email, id string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "此接口只支持保存原排队事实")
		return
	}
	var input ExecutionPlanWait
	if decodePlanBody(w, r, &input) != nil || input.PlanID != id || !executionPlanUUID.MatchString(input.RequestID) {
		writeError(w, 400, "请提供原计划排队编号和时间")
		return
	}
	bound, err := s.agents.HasActiveBinding(email, input.MachineID)
	if err != nil {
		writeError(w, 503, "电脑绑定暂时无法核对")
		return
	}
	if !bound {
		writeError(w, 403, "只有指定的绑定电脑可以登记排队")
		return
	}
	result, err := s.store.RecordWait(r.Context(), tenant, email, input)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "wait": result})
}
