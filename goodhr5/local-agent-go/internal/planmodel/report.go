// 本文件定义 HRPlus 执行报告安全快照，区分已确认数量、结果待核对和同步等待，不包含凭证。
package planmodel

import (
	"fmt"
	"time"
)

// ReportAction 区分动作结束状态及实际确认、未知、跳过、失败数量。
type ReportAction struct {
	State     string `json:"state"`
	Confirmed int64  `json:"confirmed"`
	Unknown   int64  `json:"unknown"`
	Skipped   int64  `json:"skipped"`
	Failed    int64  `json:"failed"`
}

// ReportItem 保留独立执行项和原 TaskRun，重复岗位不合并。
type ReportItem struct {
	ID               string                  `json:"id"`
	ItemID           string                  `json:"item_id"`
	TaskRunID        string                  `json:"task_run_id,omitempty"`
	PositionID       string                  `json:"position_id"`
	Order            int                     `json:"order"`
	State            string                  `json:"state"`
	Scanned          int64                   `json:"scanned"`
	DetailsAvailable bool                    `json:"details_available"`
	Actions          map[string]ReportAction `json:"actions"`
	Information      map[string]ReportAction `json:"information"`
}

// Report 保留生成时的原运行摘要，后续同步状态更新不得替换这份原快照。
type Report struct {
	SchemaVersion     int          `json:"schema_version"`
	RunID             string       `json:"run_id"`
	PlanID            string       `json:"plan_id"`
	ActivationID      string       `json:"activation_id"`
	ConfigVersion     int64        `json:"config_version"`
	RunSequence       int64        `json:"run_sequence"`
	ExecutionDate     string       `json:"execution_date"`
	PlanName          string       `json:"plan_name"`
	Kind              string       `json:"kind"`
	RunState          string       `json:"run_state"`
	EndReason         string       `json:"end_reason"`
	SyncState         string       `json:"sync_state"`
	GeneratedAt       time.Time    `json:"generated_at"`
	FinishedAt        *time.Time   `json:"finished_at,omitempty"`
	NextNominalAt     *time.Time   `json:"next_nominal_at,omitempty"`
	Items             []ReportItem `json:"items"`
	UnfinishedItemIDs []string     `json:"unfinished_item_ids"`
}

// Validate 拒绝不完整报告及负数量，不把待核对动作视为已确认完成。
func (r Report) Validate() error {
	if r.SchemaVersion != 1 || !ValidID(r.RunID) || !ValidID(r.PlanID) || !ValidID(r.ActivationID) || r.ConfigVersion < 1 || r.RunSequence < 1 || r.GeneratedAt.IsZero() || len(r.Items) == 0 {
		return fmt.Errorf("报告原运行信息不完整")
	}
	if _, err := time.Parse("2006-01-02", r.ExecutionDate); err != nil {
		return err
	}
	kinds := map[string]string{"completed": "completed", "day_incomplete": "incomplete", "stopped": "stopped", "failed": "blocked"}
	expected, known := kinds[r.Kind]
	if !known || expected != r.RunState || r.SyncState != "confirmed" && r.SyncState != "pending" {
		return fmt.Errorf("报告结果或同步状态不支持")
	}
	seen := map[string]bool{}
	for index, item := range r.Items {
		if !ValidID(item.ID) || item.ItemID == "" || item.PositionID == "" || item.Order != index || seen[item.ID] || item.Scanned < 0 || item.TaskRunID != "" && !ValidID(item.TaskRunID) {
			return fmt.Errorf("报告执行项身份不完整")
		}
		seen[item.ID] = true
		for _, stats := range []map[string]ReportAction{item.Actions, item.Information} {
			for _, value := range stats {
				if value.Confirmed < 0 || value.Unknown < 0 || value.Skipped < 0 || value.Failed < 0 {
					return fmt.Errorf("报告数量不能小于零")
				}
			}
		}
	}
	unfinished := map[string]bool{}
	for _, id := range r.UnfinishedItemIDs {
		if !seen[id] || unfinished[id] {
			return fmt.Errorf("报告未完成项不属于原运行")
		}
		unfinished[id] = true
	}
	return nil
}
