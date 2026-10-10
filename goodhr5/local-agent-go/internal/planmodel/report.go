// 本文件定义 HRPlus 执行报告安全快照，区分已确认数量、结果待核对和同步等待，不包含凭证。
package planmodel

import "time"

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
