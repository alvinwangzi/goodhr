// 本文件用可控时间验证 HRPlus 单岗位消息到期、停止、批次和扫描公平性。
package actiondispatch

import (
	"testing"
	"time"
)

// TestSchedulerFairness 验证首次检查、优先回复、复打逾期与连续两个消息批次后扫描。
func TestSchedulerFairness(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	s := New(true)
	if s.Next(now, Work{Greeting: true}) != CheckMessages {
		t.Fatal("首次应检查")
	}
	s.Checked(now)
	w := Work{Greeting: true, Reply: true, ReGreet: true, ReGreetDue: now}
	if s.Next(now, w) != Reply {
		t.Fatal("优先回复未生效")
	}
	s.Completed(Reply)
	w.ReGreetDue = now.Add(-time.Minute)
	if s.Next(now, w) != ReGreet {
		t.Fatal("到期复打被持续回复阻塞")
	}
	s.Completed(ReGreet)
	if s.Next(now, w) != Greeting {
		t.Fatal("扫描被消息饿死")
	}
	s.Completed(Greeting)
	if s.Next(now.Add(60*time.Second), w) != CheckMessages {
		t.Fatal("定时检查未触发")
	}
	s.Stop()
	if s.Next(now, w) != Done {
		t.Fatal("停止后仍领取")
	}
}

// TestCurrentWorkEnds 验证无当前工作立即结束，不等待未来复打。
func TestCurrentWorkEnds(t *testing.T) {
	s := New(false)
	now := time.Now()
	s.Checked(now)
	if s.Next(now, Work{}) != Done {
		t.Fatal("不应长期挂着")
	}
}

// TestScanPriorityBetweenChecks 验证关闭优先回复后按周期穿插消息，其余机会用于找简历。
func TestScanPriorityBetweenChecks(t *testing.T) {
	s := New(false)
	now := time.Now()
	s.Checked(now)
	w := Work{Greeting: true, Reply: true, ReGreet: true, ReGreetDue: now}
	s.Completed(ReGreet)
	s.Completed(Reply)
	if s.Next(now, w) != Greeting {
		t.Fatal("两个批次后未让出扫描")
	}
	s.Completed(Greeting)
	if s.Next(now.Add(time.Second), w) != Greeting {
		t.Fatal("关闭优先回复后仍持续抢占扫描")
	}
	if s.Next(now.Add(time.Minute), w) != CheckMessages {
		t.Fatal("到期未检查")
	}
}

// TestAnchorsAndBatchBoundary 验证不足三人的锚点、顺序变化及完整会话结束后批次上限。
func TestAnchorsAndBatchBoundary(t *testing.T) {
	if !AnchorsMatch([]string{"a", "b"}, []string{"a", "b"}) || AnchorsMatch(nil, nil) || AnchorsMatch([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("锚点比较错误")
	}
	now := time.Now()
	if BatchLimit(now, now, 2) || !BatchLimit(now, now, 3) || !BatchLimit(now, now.Add(time.Minute), 1) {
		t.Fatal("批次限制错误")
	}
}
