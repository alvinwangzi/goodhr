// 本文件验证 HRPlus 父消息服务原来源、轮换预算、未来项保护及损坏快照不能接管当前调度。
package planrunner

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestMessageServicesSnapshotRoundTrip 验证待处理与扫描预算恢复后选择保持一致，错任务拒绝。
func TestMessageServicesSnapshotRoundTrip(t *testing.T) {
	run := serviceRunFixture(t)
	original := NewMessageServices("fixture-owner")
	if err := original.Sync(run, servicePlatform); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	for range 2 {
		choice := original.Next(now, true, true, false)
		if err := original.Observed(choice, true, now, false); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := original.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := original.Snapshot()
	if err != nil || !bytes.Equal(raw, repeated) {
		t.Fatal("同事实快照排序不确定")
	}
	restored := NewMessageServices("fixture-owner")
	if err = restored.Restore(raw, run, servicePlatform); err != nil {
		t.Fatal(err)
	}
	if restored.Next(now, true, true, false).Kind != "scan" {
		t.Fatal("父扫描预算被重置")
	}
	original.Scanned()
	restored.Scanned()
	want, got := original.Next(now, false, true, false), restored.Next(now, false, true, false)
	if want != got || got.ItemRunID == run.Items[1].ID {
		t.Fatal("原工作归属改变或未来项激活", want, got)
	}
	var value MessageServicesSnapshot
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value.Services[0].Sources[0].TaskID = "wrong-task"
	invalid, _ := json.Marshal(value)
	if err = restored.Restore(invalid, run, servicePlatform); err == nil {
		t.Fatal("错误任务接管消息服务")
	}
	if after := restored.Next(now, false, true, false); after != got {
		t.Fatal("损坏恢复修改了当前服务")
	}
	if err = NewMessageServices("other-owner").Restore(raw, run, servicePlatform); err == nil {
		t.Fatal("另一账号恢复原服务")
	}
	changed := run
	changed.ExecutionDate = "2026-10-11"
	if err = restored.Restore(raw, changed, servicePlatform); err == nil {
		t.Fatal("次日沿用前一天轮换")
	}
}
