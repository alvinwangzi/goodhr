// 本文件定义 HRPlus M2 计划、运行和执行项的数据契约；云端只保存编排与执行事实，不操作招聘页面。
package httpapi

import (
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

// ExecutionPlanWindow 使用当天分钟值描述名义窗口，随机收尾保存在运行数据而非配置中。
type ExecutionPlanWindow struct {
	Order       int `json:"order"`
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// ExecutionPlanSchedule 定义周期与时区；星期使用周一一至周日七。
type ExecutionPlanSchedule struct {
	Cycle     string                `json:"cycle"`
	Timezone  string                `json:"timezone"`
	OnceDate  string                `json:"once_date,omitempty"`
	StartDate string                `json:"start_date,omitempty"`
	EndDate   string                `json:"end_date,omitempty"`
	Weekdays  []int                 `json:"weekdays,omitempty"`
	Windows   []ExecutionPlanWindow `json:"windows"`
}

// ExecutionPlanItem 使用独立编号保留同岗位的多次编排，不按岗位合并。
type ExecutionPlanItem struct {
	ID              string   `json:"id"`
	PositionID      string   `json:"position_id"`
	Order           int      `json:"order"`
	Actions         []string `json:"actions"`
	PrioritizeReply bool     `json:"prioritize_reply"`
}

// ExecutionPlanConfig 是可版本化的用户编排，不包含登录令牌或执行占用凭证。
type ExecutionPlanConfig struct {
	Name     string                `json:"name"`
	Schedule ExecutionPlanSchedule `json:"schedule"`
	Items    []ExecutionPlanItem   `json:"items"`
}

// ExecutionPlan 保存所有者、执行设备与长期启用状态，当天未完成不改变启用状态。
type ExecutionPlan struct {
	ID            string              `json:"id"`
	TenantID      string              `json:"tenant_id"`
	UserEmail     string              `json:"user_email"`
	MachineID     string              `json:"machine_id"`
	Version       int64               `json:"version"`
	StateSequence int64               `json:"state_sequence"`
	ActivationID  string              `json:"activation_id"`
	State         string              `json:"state"`
	StopRequested bool                `json:"stop_requested"`
	Config        ExecutionPlanConfig `json:"config"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

// ExecutionPlanRun 保存一批次一个本地执行日的运行，与长期计划状态分离。
type ExecutionPlanRun struct {
	ID            string              `json:"id"`
	PlanID        string              `json:"plan_id"`
	ActivationID  string              `json:"activation_id"`
	ExecutionDate string              `json:"execution_date"`
	ConfigVersion int64               `json:"config_version"`
	Sequence      int64               `json:"sequence"`
	State         string              `json:"state"`
	CurrentItem   int                 `json:"current_item"`
	Snapshot      ExecutionPlanConfig `json:"snapshot"`
	EndReason     string              `json:"end_reason"`
	OwnerID       string              `json:"owner_id,omitempty"`
}

// Validate 校验用户配置结构，岗位权限、设备和平台能力由计划服务读取真实数据后再校验。
func (c ExecutionPlanConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("请填写计划名称")
	}
	s := c.Schedule
	if s.Cycle != "once" && s.Cycle != "daily" && s.Cycle != "weekly" {
		return fmt.Errorf("计划周期不支持")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil || s.Timezone == "" || s.Timezone == "Local" {
		return fmt.Errorf("计划时区无法识别")
	}
	for _, date := range []string{s.OnceDate, s.StartDate, s.EndDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("计划日期格式不正确")
			}
		}
	}
	if s.Cycle == "once" && s.OnceDate == "" {
		return fmt.Errorf("一次性计划需要执行日期")
	}
	if s.StartDate != "" && s.EndDate != "" && s.StartDate > s.EndDate {
		return fmt.Errorf("有效日期范围不正确")
	}
	if s.Cycle == "once" && ((s.StartDate != "" && s.OnceDate < s.StartDate) || (s.EndDate != "" && s.OnceDate > s.EndDate)) {
		return fmt.Errorf("一次性日期不在有效期内")
	}
	days := map[int]bool{}
	for _, day := range s.Weekdays {
		if day < 1 || day > 7 || days[day] {
			return fmt.Errorf("星期必须为一至七且不重复")
		}
		days[day] = true
	}
	if s.Cycle == "weekly" && len(days) == 0 {
		return fmt.Errorf("每周计划至少选择一天")
	}
	if len(s.Windows) == 0 {
		return fmt.Errorf("至少添加一个时间段")
	}
	windows := append([]ExecutionPlanWindow{}, s.Windows...)
	for index, w := range windows {
		if w.Order != index || w.StartMinute < 0 || w.EndMinute > 1440 || w.StartMinute >= w.EndMinute {
			return fmt.Errorf("时间段必须在一天内，顺序连续且开始早于结束")
		}
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].StartMinute < windows[j].StartMinute })
	for i := 1; i < len(windows); i++ {
		if windows[i].StartMinute < windows[i-1].EndMinute {
			return fmt.Errorf("同一计划内时间段不能重叠")
		}
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("至少添加一个岗位执行项")
	}
	ids := map[string]bool{}
	for index, item := range c.Items {
		if item.ID == "" || len(item.ID) > 128 || ids[item.ID] || item.PositionID == "" || item.Order != index {
			return fmt.Errorf("执行项编号、岗位或顺序不正确")
		}
		ids[item.ID] = true
		if len(item.Actions) == 0 {
			return fmt.Errorf("每项至少选择一个动作")
		}
		actions := map[string]bool{}
		for _, action := range item.Actions {
			if (action != "greeting" && action != "auto_reply" && action != "re_greet") || actions[action] {
				return fmt.Errorf("执行动作不支持或重复")
			}
			actions[action] = true
		}
		if item.PrioritizeReply && !actions["auto_reply"] {
			return fmt.Errorf("优先回复需要勾选自动回复")
		}
	}
	return nil
}
