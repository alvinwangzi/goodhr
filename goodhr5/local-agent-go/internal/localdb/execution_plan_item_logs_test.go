// 本文件验证 HRPlus 同岗位多项及迟到日志的原归属、账号隔离、分页和真实 SQLite 重开。
package localdb

import (
	"encoding/json"
	"testing"
)

// TestPlanItemLogsOriginalSource 验证指定旧检查点写入不会被同岗位的新执行项接管。
func TestPlanItemLogsOriginalSource(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	for index := range run.Items {
		run.Items[index].TaskRunID = run.Items[index].ID
	}
	checkpoints := []ActionCheckpoint{}
	for _, item := range run.Items {
		cp, err := db.EnsurePlanActionRun(t.Context(), ActionCheckpoint{PlanRunID: run.ID, ItemRunID: item.ID, CloudRunID: item.TaskRunID, OwnerScope: "owner-A", PositionID: "same-job", Platform: "boss", ProfileScope: "fixture", TaskType: "greeting"})
		if err != nil {
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, cp)
	}
	first, err := db.AddPlanItemLog(t.Context(), "owner-A", checkpoints[0].RunID, "info", "第一项已处理候选人")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.AddPlanItemLog(t.Context(), "owner-A", checkpoints[1].RunID, "info", "第二项开始"); err != nil {
		t.Fatal(err)
	}
	late, err := db.AddPlanItemLog(t.Context(), "owner-A", checkpoints[0].RunID, "warning", "第一项迟到收尾结果")
	if err != nil || late.ItemRunID != first.ItemRunID || late.TaskRunID != first.TaskRunID {
		t.Fatal("迟到日志被新项接管", late, err)
	}
	if _, err = db.AddPlanItemLog(t.Context(), "owner-B", checkpoints[0].RunID, "info", "错误账号"); err == nil {
		t.Fatal("其他账号能写原项")
	}
	changed := checkpoints[0]
	changed.CloudRunID = "90000000-0000-0000-0000-000000000009"
	if err = db.SaveActionCheckpoint(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AddPlanItemLog(t.Context(), "owner-A", changed.RunID, "info", "不能换原任务"); err == nil {
		t.Fatal("日志原任务被改写")
	}
	items, err := db.ListPlanItemLogs(t.Context(), "owner-A", run.ID, first.ItemRunID, 0, 1)
	if err != nil || len(items) != 1 || items[0].ID != late.ID {
		t.Fatal("独立日志或排序错误", items, err)
	}
	older, err := db.ListPlanItemLogs(t.Context(), "owner-A", run.ID, first.ItemRunID, items[0].ID, 100)
	if err != nil || len(older) != 1 || older[0].ID != first.ID {
		t.Fatal("分页串项或丢记录", older, err)
	}
	foreign, err := db.ListPlanItemLogs(t.Context(), "owner-B", run.ID, first.ItemRunID, 0, 100)
	if err != nil || len(foreign) != 0 {
		t.Fatal("跨账号读取日志", foreign, err)
	}
	raw, err := json.Marshal(late)
	if err != nil || json.Valid(raw) == false {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if _, exists := body["owner_scope"]; exists {
		t.Fatal("日志输出暴露账号作用域")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.ListPlanItemLogs(t.Context(), "owner-A", run.ID, first.ItemRunID, 0, 100)
	if err != nil || len(restored) != 2 || restored[0].TaskRunID != first.TaskRunID {
		t.Fatal("数据库重开丢失原日志", restored, err)
	}
}
