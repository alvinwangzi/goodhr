// 本文件验证 HRPlus 消息公平性快照保留预算，恢复不重放过往定时周期。
package actiondispatch

import (
	"testing"
	"time"
)

// TestSchedulerSnapshotRestore 验证已处理两批后仍让出扫描，错预算或错动作拒绝恢复。
func TestSchedulerSnapshotRestore(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	original := New(true)
	original.Checked(now)
	original.Completed(Reply)
	original.Completed(ReGreet)
	restored := New(false)
	if err := restored.Restore(original.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if restored.PrioritizeReply || restored.Next(now, Work{Greeting: true, Reply: true}) != Greeting {
		t.Fatal("恢复覆盖最新策略或预算被重置")
	}
	if restored.Next(now.Add(2*time.Hour), Work{Reply: true}) != CheckMessages {
		t.Fatal("休眠后没有一次重新检查")
	}
	for _, invalid := range []Snapshot{{MessageBatches: -1}, {LastMessage: "unknown"}} {
		if err := restored.Restore(invalid); err == nil {
			t.Fatal("非法原公平性接受")
		}
	}
}
