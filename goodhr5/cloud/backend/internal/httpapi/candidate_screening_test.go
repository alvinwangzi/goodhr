// 本文件覆盖复打招呼相关的云端存储逻辑：名单过滤、计数累加、greeted_at 永不覆盖。
package httpapi

import (
	"context"
	"testing"
	"time"
)

// TestMemoryReGreetCandidates_ListFilter 验证复打名单的 4 个过滤条件：
// greeted_at 不为空 / 时间范围 / 复打次数上限 / 间隔下限。
func TestMemoryReGreetCandidates_ListFilter(t *testing.T) {
	store := NewMemoryCandidateScreeningStore()
	ctx := context.Background()
	positionID := "position-1"
	platform := "boss"
	now := time.Now()

	// A：3 天前打过招呼，未复打过 → 应命中
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-a", CandidateName: "A",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	// 把 A 的 greeted_at 平移到 3 天前，确保超过间隔下限（30 分钟）且在时间范围内（7 天）。
	store.forceShiftGreetedAt(ctx, positionID, "cand-a", now.AddDate(0, 0, -3))

	// B：10 天前打过招呼，超出 7 天时间范围 → 应过滤掉
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-b", CandidateName: "B",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert B: %v", err)
	}
	store.forceShiftGreetedAt(ctx, positionID, "cand-b", now.AddDate(0, 0, -10))

	// C：已复打过且 re_greet_count >= maxCount → 应过滤掉
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-c", CandidateName: "C",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert C: %v", err)
	}
	store.forceShiftGreetedAt(ctx, positionID, "cand-c", now.AddDate(0, 0, -3))
	if _, err := store.MarkReGreetDone(ctx, positionID, platform, []string{"cand-c"}); err != nil {
		t.Fatalf("mark C: %v", err)
	}

	// D：5 天前打过招呼，20 分钟前刚复打过，未到间隔下限 → 应过滤掉
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-d", CandidateName: "D",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert D: %v", err)
	}
	store.forceShiftGreetedAt(ctx, positionID, "cand-d", now.AddDate(0, 0, -5))
	if _, err := store.MarkReGreetDone(ctx, positionID, platform, []string{"cand-d"}); err != nil {
		t.Fatalf("mark D: %v", err)
	}
	store.forceShiftLastReGreetedAt(ctx, positionID, "cand-d", now.Add(-20*time.Minute))

	// E：auto_reply 流程写入但没 SetGreetedAt → greeted_at 为空，应过滤掉
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-e", CandidateName: "E",
		Score: 80, Status: "screened", Source: "auto_reply",
	}, positionID); err != nil {
		t.Fatalf("upsert E: %v", err)
	}

	items, err := store.ListReGreetCandidates(ctx, positionID, platform, 7, 30, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].PlatformCandidateID != "cand-a" {
		t.Fatalf("want only A, got %d items", len(items))
	}
}

// TestMemoryReGreet_MarkDoneIncrementsCount 验证 MarkReGreetDone 同时更新
// last_re_greeted_at 与 re_greet_count，且不影响其他候选人。
func TestMemoryReGreet_MarkDoneIncrementsCount(t *testing.T) {
	store := NewMemoryCandidateScreeningStore()
	ctx := context.Background()
	positionID := "position-1"
	platform := "boss"

	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-x", CandidateName: "X",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert X: %v", err)
	}
	if _, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-y", CandidateName: "Y",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID); err != nil {
		t.Fatalf("upsert Y: %v", err)
	}

	affected, err := store.MarkReGreetDone(ctx, positionID, platform, []string{"cand-x"})
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if affected != 1 {
		t.Fatalf("期望影响 1 行，实际 %d", affected)
	}

	x, err := store.FindScreening(ctx, positionID, platform, "cand-x")
	if err != nil {
		t.Fatalf("find X: %v", err)
	}
	if x.ReGreetCount != 1 || x.LastReGreetedAt == nil {
		t.Fatalf("X 复打计数或时间戳未更新：%+v", x)
	}
	y, err := store.FindScreening(ctx, positionID, platform, "cand-y")
	if err != nil {
		t.Fatalf("find Y: %v", err)
	}
	if y.ReGreetCount != 0 || y.LastReGreetedAt != nil {
		t.Fatalf("Y 不应被修改：%+v", y)
	}
}

// TestMemoryScreening_UpsertGreetedAtNeverOverwritten 验证 greeted_at 在 upsert 冲突时永不覆盖。
// 复打招呼名单正确性的核心：auto_reply 流程的后续 upsert 不能把打招呼写入的 greeted_at 清掉。
func TestMemoryScreening_UpsertGreetedAtNeverOverwritten(t *testing.T) {
	store := NewMemoryCandidateScreeningStore()
	ctx := context.Background()
	positionID := "position-1"
	platform := "boss"

	// 第一次：打招呼流程写入 greeted_at。
	first, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-z", CandidateName: "Z",
		Score: 80, Status: "passed", Source: "greeting", SetGreetedAt: true,
	}, positionID)
	if err != nil {
		t.Fatalf("upsert greeting: %v", err)
	}
	if first.GreetedAt == nil {
		t.Fatalf("打招呼流程应写入 greeted_at")
	}
	original := *first.GreetedAt

	// 第二次：auto_reply 流程 upsert，不带 SetGreetedAt；greeted_at 必须保留。
	second, err := store.UpsertScreening(ctx, CandidateScreeningUpsert{
		Platform: platform, PlatformCandidateID: "cand-z", CandidateName: "Z",
		Score: 85, Status: "screened", Source: "auto_reply",
	}, positionID)
	if err != nil {
		t.Fatalf("upsert auto_reply: %v", err)
	}
	if second.GreetedAt == nil || !second.GreetedAt.Equal(original) {
		t.Fatalf("auto_reply upsert 不应覆盖 greeted_at，原值=%v 实际=%v", original, second.GreetedAt)
	}
	if second.Score != 85 || second.Source != "auto_reply" {
		t.Fatalf("其他字段应被更新：%+v", second)
	}
}

// forceShiftGreetedAt 测试辅助：把内存记录的 greeted_at 平移到指定时间，
// 用于模拟"超出时间范围"等场景。
func (s *MemoryCandidateScreeningStore) forceShiftGreetedAt(_ context.Context, positionID, platformCandidateID string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.PositionID == positionID && item.PlatformCandidateID == platformCandidateID {
			item.GreetedAt = &t
			return
		}
	}
}

// forceShiftLastReGreetedAt 测试辅助：把内存记录的 last_re_greeted_at 平移到指定时间。
func (s *MemoryCandidateScreeningStore) forceShiftLastReGreetedAt(_ context.Context, positionID, platformCandidateID string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.PositionID == positionID && item.PlatformCandidateID == platformCandidateID {
			item.LastReGreetedAt = &t
			return
		}
	}
}
