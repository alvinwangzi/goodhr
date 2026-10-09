// 本文件定义 HRPlus 本地计划与云端进度契约，不包含登录令牌或占用凭证，复用本地计划时钟。
package planmodel

import (
	"fmt"
	"goodhr5/local-agent-go/internal/planclock"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Item 是独立编排项，同岗位重复出现仍保持不同 ID。
type Item struct {
	ID              string   `json:"id"`
	PositionID      string   `json:"position_id"`
	Order           int      `json:"order"`
	Actions         []string `json:"actions"`
	PrioritizeReply bool     `json:"prioritize_reply"`
}

// Config 保存用户编排，日期与时段直接使用已验证的本地时钟契约。
type Config struct {
	Name     string             `json:"name"`
	Schedule planclock.Schedule `json:"schedule"`
	Items    []Item             `json:"items"`
}

// Plan 保存真实归属、指定设备、启用批次和递增状态，缓存不代表启动许可。
type Plan struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	UserEmail     string    `json:"user_email"`
	MachineID     string    `json:"machine_id"`
	Version       int64     `json:"version"`
	StateSequence int64     `json:"state_sequence"`
	ActivationID  string    `json:"activation_id"`
	State         string    `json:"state"`
	StopRequested bool      `json:"stop_requested"`
	Config        Config    `json:"config"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ActionProgress 区分已确认完成数量和未知结果，未知不计为成功。
type ActionProgress struct {
	State        string `json:"state"`
	Count        int64  `json:"count"`
	UnknownCount int64  `json:"unknown_count"`
}

// ItemRun 保存独立项的原快照与消息激活状态，不通过岗位 ID 合并。
type ItemRun struct {
	ID        string                    `json:"id"`
	ItemID    string                    `json:"item_id"`
	Order     int                       `json:"order"`
	TaskRunID string                    `json:"task_run_id,omitempty"`
	Snapshot  Item                      `json:"snapshot"`
	State     string                    `json:"state"`
	Actions   map[string]ActionProgress `json:"actions"`
}

// Run 保存当天运行事实，状态补传和恢复均须保留原编号与序号。
type Run struct {
	ID            string     `json:"id"`
	PlanID        string     `json:"plan_id"`
	ActivationID  string     `json:"activation_id"`
	ExecutionDate string     `json:"execution_date"`
	ConfigVersion int64      `json:"config_version"`
	Sequence      int64      `json:"sequence"`
	State         string     `json:"state"`
	CurrentItem   int        `json:"current_item"`
	Snapshot      Config     `json:"snapshot"`
	EndReason     string     `json:"end_reason"`
	OwnerID       string     `json:"owner_id,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Items         []ItemRun  `json:"items"`
}

// Owner 仅展示占用身份与状态，不含可释放任务的凭证。
type Owner struct {
	OwnerID   string `json:"owner_id"`
	OwnerType string `json:"owner_type"`
	MachineID string `json:"machine_id"`
	State     string `json:"state"`
}

// Permit 保存已核对的运行和占用快照，是否实际启动由本地主流程决定。
type Permit struct {
	Run   Run   `json:"run"`
	Owner Owner `json:"owner"`
}

// ItemUpdate 只上报原项身份与进度，不允许修改编排快照。
type ItemUpdate struct {
	ID      string                    `json:"id"`
	ItemID  string                    `json:"item_id"`
	State   string                    `json:"state"`
	Actions map[string]ActionProgress `json:"actions"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidID 核对由系统生成的运行或请求编号，不限制用户定义的编排项名称。
func ValidID(id string) bool { return uuidPattern.MatchString(id) }

// Validate 校验完整编排，避免缓存半套配置或未知动作后自动执行。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Items) == 0 {
		return fmt.Errorf("计划名称或执行项缺失")
	}
	if err := c.Schedule.Validate(); err != nil {
		return err
	}
	ids := map[string]bool{}
	for index, item := range c.Items {
		if item.ID == "" || len(item.ID) > 128 || ids[item.ID] || item.PositionID == "" || item.Order != index || len(item.Actions) == 0 {
			return fmt.Errorf("计划执行项编号或顺序不正确")
		}
		ids[item.ID] = true
		actions := map[string]bool{}
		for _, action := range item.Actions {
			if actions[action] || (action != "greeting" && action != "auto_reply" && action != "re_greet") {
				return fmt.Errorf("计划动作不支持或重复")
			}
			actions[action] = true
		}
		if item.PrioritizeReply && !actions["auto_reply"] {
			return fmt.Errorf("优先回复需要自动回复动作")
		}
	}
	return nil
}

// Validate 校验计划缓存的真实身份和单调序号，启用状态必须带有效批次。
func (p Plan) Validate() error {
	if !ValidID(p.ID) || p.UserEmail == "" || p.MachineID == "" || p.Version < 1 || p.StateSequence < 1 || (p.State != "enabled" && p.State != "stopped" && p.State != "disabled") || (p.State == "enabled" && !ValidID(p.ActivationID)) {
		return fmt.Errorf("云端计划身份或状态不完整")
	}
	return p.Config.Validate()
}

// Validate 核对原运行、完整独立项与非负计数，不能接受泛成功响应代替实际许可。
func (r Run) Validate() error {
	if !ValidID(r.ID) || !ValidID(r.PlanID) || !ValidID(r.ActivationID) || r.Sequence < 1 || r.ConfigVersion < 1 || r.CurrentItem < 0 || r.CurrentItem > len(r.Snapshot.Items) {
		return fmt.Errorf("云端运行身份或进度不完整")
	}
	if _, err := time.Parse("2006-01-02", r.ExecutionDate); err != nil {
		return fmt.Errorf("云端运行日期不正确")
	}
	switch r.State {
	case "pending", "waiting_resource", "starting", "running", "draining", "waiting_window", "completed", "incomplete", "stopped", "blocked":
	default:
		return fmt.Errorf("云端运行状态不支持")
	}
	if err := r.Snapshot.Validate(); err != nil {
		return err
	}
	if len(r.Items) != len(r.Snapshot.Items) {
		return fmt.Errorf("云端执行项进度不完整")
	}
	ids := map[string]bool{}
	for index, item := range r.Items {
		if !ValidID(item.ID) || ids[item.ID] || item.Order != index || item.ItemID != r.Snapshot.Items[index].ID || !reflect.DeepEqual(item.Snapshot, r.Snapshot.Items[index]) || len(item.Actions) != len(item.Snapshot.Actions) {
			return fmt.Errorf("云端执行项身份与快照不匹配")
		}
		ids[item.ID] = true
		switch item.State {
		case "pending", "running", "completed", "failed", "stopped":
		default:
			return fmt.Errorf("云端执行项状态不支持")
		}
		for _, action := range item.Snapshot.Actions {
			progress, exists := item.Actions[action]
			if !exists || progress.Count < 0 || progress.UnknownCount < 0 {
				return fmt.Errorf("云端动作计数不完整")
			}
			if (item.State == "pending" || progress.State == "pending") && (progress.State != "pending" || progress.Count != 0 || progress.UnknownCount != 0) {
				return fmt.Errorf("未开始动作含执行状态或数量")
			}
			if index > r.CurrentItem && progress.State == "active" {
				return fmt.Errorf("后续动作被提前激活")
			}
			switch progress.State {
			case "pending", "active", "completed", "stopped":
			default:
				return fmt.Errorf("云端动作状态不支持")
			}
		}
	}
	return nil
}
