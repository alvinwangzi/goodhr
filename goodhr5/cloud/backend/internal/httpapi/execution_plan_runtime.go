// 本文件展示 HRPlus 名义时间和已确认运行事实，不发出本地调度或招聘命令。
package httpapi

import (
	"net/http"
	"time"
)

// ExecutionPlanRuntimeView 将当前名义安排与最后已确认事实分开，不能作为页面启动许可。
type ExecutionPlanRuntimeView struct {
	ExecutionPlanRuntimeSnapshot
	ObservedAt  time.Time          `json:"observed_at"`
	NominalAt   *time.Time         `json:"nominal_at,omitempty"`
	WaitReason  string             `json:"wait_reason"`
	CurrentRun  *ExecutionPlanRun  `json:"current_run,omitempty"`
	Waiting     *ExecutionPlanWait `json:"waiting,omitempty"`
	WaitSeconds *int64             `json:"wait_seconds,omitempty"`
}

// runtimeDateAllowed 只用于显示原配置日期，执行授权继续由本地计划时钟核对。
func runtimeDateAllowed(s ExecutionPlanSchedule, day time.Time) bool {
	date := day.Format("2006-01-02")
	if s.StartDate != "" && date < s.StartDate || s.EndDate != "" && date > s.EndDate {
		return false
	}
	if s.Cycle == "once" {
		return date == s.OnceDate
	}
	if s.Cycle == "weekly" {
		weekday := (int(day.Weekday())+6)%7 + 1
		for _, d := range s.Weekdays {
			if d == weekday {
				return true
			}
		}
		return false
	}
	return s.Cycle == "daily"
}

// buildPlanRuntimeView 处理当前窗口、当天已结束和远期生效日期，不将随机收尾当新开始时间。
func buildPlanRuntimeView(snapshot ExecutionPlanRuntimeSnapshot, now time.Time) (ExecutionPlanRuntimeView, error) {
	view := ExecutionPlanRuntimeView{ExecutionPlanRuntimeSnapshot: snapshot, ObservedAt: now.UTC(), WaitReason: "stopped"}
	plan := snapshot.Plan
	if err := plan.Config.Validate(); err != nil {
		return view, err
	}
	if plan.StopRequested || plan.State != "enabled" {
		for _, run := range snapshot.Runs {
			if run.ActivationID == plan.ActivationID && run.ConfigVersion == plan.Version {
				copy := run
				view.CurrentRun = &copy
				break
			}
		}
	}
	if plan.StopRequested {
		view.WaitReason = "stopping"
		return view, nil
	}
	if plan.State != "enabled" {
		return view, nil
	}
	s := plan.Config.Schedule
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return view, err
	}
	localNow := now.In(loc)
	date := localNow.Format("2006-01-02")
	finishedToday := false
	for _, run := range snapshot.Runs {
		if run.ActivationID != plan.ActivationID || run.ConfigVersion != plan.Version {
			continue
		}
		if activeExecutionPlanState(run.State) && run.State != "pending" && run.State != "waiting_resource" && run.State != "waiting_window" {
			copy := run
			view.CurrentRun = &copy
			view.WaitReason = "executing"
			if run.ExecutionDate != date {
				view.WaitReason = "recovery_required"
			}
			return view, nil
		}
		if run.ExecutionDate == date {
			copy := run
			view.CurrentRun = &copy
			finishedToday = !activeExecutionPlanState(run.State)
			break
		}
	}
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	if finishedToday {
		day = day.AddDate(0, 0, 1)
	}
	if s.StartDate != "" && day.Format("2006-01-02") < s.StartDate {
		day, _ = time.ParseInLocation("2006-01-02", s.StartDate, loc)
	}
	if s.Cycle == "once" {
		once, _ := time.ParseInLocation("2006-01-02", s.OnceDate, loc)
		if once.Before(day) {
			view.WaitReason = "no_future_window"
			return view, nil
		}
		day = once
	}
	// 生效日期已直接跳转，之后一周内一定覆盖任何有效的每周配置。
	for offset := 0; offset < 8; offset++ {
		candidate := day.AddDate(0, 0, offset)
		if !runtimeDateAllowed(s, candidate) {
			continue
		}
		for _, window := range s.Windows {
			start := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), window.StartMinute/60, window.StartMinute%60, 0, 0, loc)
			end := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), window.EndMinute/60, window.EndMinute%60, 0, 0, loc)
			if !now.Before(end) {
				continue
			}
			if view.NominalAt == nil || start.Before(*view.NominalAt) {
				copy := start
				view.NominalAt = &copy
			}
		}
		if view.NominalAt != nil {
			break
		}
	}
	view.WaitReason = "waiting_time"
	if view.NominalAt == nil {
		view.WaitReason = "no_future_window"
	} else if !now.Before(*view.NominalAt) {
		view.WaitReason = "waiting_start"
		if snapshot.AccountOwner != nil || snapshot.LegacyBusy {
			view.WaitReason = "account_busy"
		}
	}
	if !finishedToday {
		for _, wait := range snapshot.Waits {
			if wait.ActivationID != plan.ActivationID || wait.ConfigVersion != plan.Version || wait.TriggeredAt.In(loc).Format("2006-01-02") != date || now.Before(wait.TriggeredAt) {
				continue
			}
			if view.CurrentRun != nil && view.CurrentRun.StartedAt != nil && !wait.TriggeredAt.After(*view.CurrentRun.StartedAt) {
				continue
			}
			if view.Waiting == nil || wait.TriggeredAt.Before(view.Waiting.TriggeredAt) {
				copy := clonePlanWait(wait)
				view.Waiting = &copy
			}
		}
		if view.Waiting != nil {
			view.WaitReason = "queued"
			if view.NominalAt == nil || view.NominalAt.In(loc).Format("2006-01-02") != date {
				view.WaitReason = "queue_day_missed"
			} else if now.Before(*view.NominalAt) {
				view.WaitReason = "queue_waiting_time"
			}
			if view.Waiting.QueuedAt != nil && !now.Before(*view.Waiting.QueuedAt) {
				seconds := int64(now.Sub(*view.Waiting.QueuedAt) / time.Second)
				view.WaitSeconds = &seconds
			}
		}
	}
	return view, nil
}

// runtimeView 返回当前只读快照；读取不能领取任务、发送停止或重置原运行。
func (s *ExecutionPlanService) runtimeView(w http.ResponseWriter, r *http.Request, tenant, email, id string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "此接口只支持读取计划状态")
		return
	}
	snapshot, err := s.store.RuntimeSnapshot(r.Context(), tenant, email, id)
	if err != nil {
		writePlanStoreError(w, err)
		return
	}
	view, err := buildPlanRuntimeView(snapshot, time.Now())
	if err != nil {
		writeError(w, 500, "计划时间暂时无法核对")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "runtime": view})
}
