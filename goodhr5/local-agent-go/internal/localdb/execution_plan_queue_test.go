// 本文件使用真实 SQLite 验证 HRPlus M2 排队顺序、重复请求和数据库重开后的收尾时间。
package localdb

import (
	"errors"
	"goodhr5/local-agent-go/internal/config"
	"testing"
	"time"
)

// TestPlanQueueRestart 验证同秒请求按稳定序号排队，重复请求不改变原事实，其他账号互不混用。
func TestPlanQueueRestart(t *testing.T) {
	db, cfg := openReplyDB(t)
	base := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	first := PlanWaitingRequest{OwnerScope: "A", RequestID: "one", PlanID: "p", ActivationID: "batch", TriggeredAt: base}
	second := first
	second.RequestID = "two"
	second.PlanID = "other-plan"
	a, err := db.EnqueuePlanRequest(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.EnqueuePlanRequest(t.Context(), second)
	if err != nil || b.Sequence <= a.Sequence {
		t.Fatal("同秒排队顺序丢失", err)
	}
	retry, err := db.EnqueuePlanRequest(t.Context(), first)
	if err != nil || retry.Sequence != a.Sequence {
		t.Fatal("重试重新入队", err)
	}
	first.TriggeredAt = base.Add(time.Second)
	if _, err := db.EnqueuePlanRequest(t.Context(), first); !errors.Is(err, ErrPlanRequestConflict) {
		t.Fatal("旧请求篡改了触发时间")
	}
	if _, err := db.NextPlanRequest(t.Context(), "B"); err == nil {
		t.Fatal("另一账号读取了队列")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(&config.Config{DataDir: cfg.DataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	head, err := reopened.NextPlanRequest(t.Context(), "A")
	if err != nil || head.Sequence != a.Sequence || head.RequestID != "one" {
		t.Fatal("重开丢失 FIFO", err)
	}
}

// TestPlanGraceDrawOnce 验证窗口延迟持久保存，重复读取不重新随机，错误值不留下记录。
func TestPlanGraceDrawOnce(t *testing.T) {
	db, _ := openReplyDB(t)
	draws := 0
	sample := func() time.Duration { draws++; return 4 * time.Minute }
	a, err := db.PlanWindowGrace(t.Context(), "A", "p", "2026-10-10", 540, 720, sample)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.PlanWindowGrace(t.Context(), "A", "p", "2026-10-10", 540, 720, sample)
	if err != nil || a != b || draws != 1 {
		t.Fatal("重复随机了收尾", err)
	}
	if _, err := db.PlanWindowGrace(t.Context(), "A", "p", "2026-10-10", 810, 1200, func() time.Duration { return time.Minute }); err == nil {
		t.Fatal("错误随机值被接受")
	}
	if _, err := db.PlanWindowGrace(t.Context(), "A", "p", "2026-10-10", 810, 1200, sample); err != nil {
		t.Fatal("失败事务未回滚", err)
	}
}
