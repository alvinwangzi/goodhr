// 本文件验证 HRPlus 重复岗位执行项的 M1 检查点独立、真实重开恢复和归属不可改写。
package localdb

import (
	"errors"
	"goodhr5/local-agent-go/internal/config"
	"testing"
)

// TestPlanActionCheckpointRestart 验证同岗位独立项保存不同数量，恢复不重置发送事实或原 TaskRun。
func TestPlanActionCheckpointRestart(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	input := ActionCheckpoint{PlanRunID: "20000000-0000-0000-0000-000000000001", ItemRunID: "50000000-0000-0000-0000-000000000001", CloudRunID: "70000000-0000-0000-0000-000000000001", OwnerScope: "fixture-owner", PositionID: "same-position", Platform: "boss", ProfileScope: "fixture-profile", TaskType: "greeting", PositionSnapshot: map[string]any{"name": "fixture", "match_limit": 10}}
	first, err := db.EnsurePlanActionRun(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	first.Scanned, first.Greeted = 4, 1
	first.Anchors = []string{"real-fixture-candidate"}
	if err = db.SaveActionCandidate(t.Context(), first, "real-fixture-candidate", "completed", "confirmed"); err != nil {
		t.Fatal(err)
	}
	secondInput := input
	secondInput.ItemRunID = "50000000-0000-0000-0000-000000000002"
	secondInput.CloudRunID = "70000000-0000-0000-0000-000000000002"
	second, err := db.EnsurePlanActionRun(t.Context(), secondInput)
	if err != nil || second.RunID == first.RunID || second.Scanned != 0 || second.Greeted != 0 {
		t.Fatal("重复岗位合并或继承了数量", err)
	}
	wrong := first
	wrong.OwnerScope = "other-owner"
	if err = db.SaveActionCheckpoint(t.Context(), wrong); err == nil {
		t.Fatal("进度保存改变原计划归属")
	}
	if err = db.SaveActionCandidate(t.Context(), wrong, "other-candidate", "completed", "fixture"); err == nil {
		t.Fatal("候选人保存改变原计划归属")
	}
	changed := input
	changed.CloudRunID = second.CloudRunID
	if _, err = db.EnsurePlanActionRun(t.Context(), changed); !errors.Is(err, ErrPlanRequestConflict) {
		t.Fatal("原执行项可改绑 TaskRun", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.EnsurePlanActionRun(t.Context(), input)
	if err != nil || restored.Scanned != 4 || restored.Greeted != 1 || restored.RunID != first.RunID || len(restored.Anchors) != 1 {
		t.Fatal("恢复重置本项进度", err)
	}
	ids, err := reopened.ActionCompletedIDs(t.Context(), restored.RunID, input.PositionID)
	if err != nil || len(ids) != 1 || ids[0] != "real-fixture-candidate" {
		t.Fatal("恢复丢失原候选人事实", err)
	}
	ids, err = reopened.ActionCompletedIDs(t.Context(), second.RunID, input.PositionID)
	if err != nil || len(ids) != 0 {
		t.Fatal("第二项混入第一项候选人记录", err)
	}
}
