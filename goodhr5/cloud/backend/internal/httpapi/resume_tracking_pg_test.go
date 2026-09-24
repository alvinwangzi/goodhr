// 本文件在独立测试 PostgreSQL 中验证简历进度的真实 SQL、跨岗位隔离、并发去重与正文保护。
package httpapi

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestResumeTrackingPostgres 验证同一候选人跨岗位的进度隔离，以及重复同步不覆盖详细简历正文。
func TestResumeTrackingPostgres(t *testing.T) {
	dsn := os.Getenv("GOODHR_RESUME_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未配置独立简历跟踪测试数据库")
	}
	t.Chdir("../..")
	db, err := (Config{PostgresDSN: dsn}).PostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	email := "resume-test-" + time.Now().Format("150405.000000000") + "@example.com"
	tenant, err := NewPostgresTenantStore(db).GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	positions := NewPostgresPositionStore(db)
	p1, err := positions.SavePosition(Position{UserEmail: email, PlatformID: "boss", Name: "岗位一"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := positions.SavePosition(Position{UserEmail: email, PlatformID: "boss", Name: "岗位二"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCandidateStore(db)
	// 先保存一份完整简历，稍后验证跟踪同步不会清空 raw_text。
	profile, err := store.SaveCandidateProfile(CandidateProfileInput{
		UserEmail: email, PlatformID: "boss", PlatformCandidateID: "chat:one",
		CandidateName: "测试同名", RawText: "已保存的完整简历正文",
	})
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	item := ResumeTrackingInput{ID: "chat:one", Name: "测试同名", State: "pending", Score: float64Ptr(85), Reason: "匹配岗位", UpdatedAt: at}
	// 并发写入同一岗位，验证事务去重不会产生多条关系或多个事件。
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.SaveResumeTracking(p1, item); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	// 同一名候选人在另一岗位推进到已下载，验证岗位级隔离。
	item2 := item
	item2.State, item2.UpdatedAt = "downloaded", at.Add(time.Second)
	if err := store.SaveResumeTracking(p2, item2); err != nil {
		t.Fatal(err)
	}

	// 简历库按岗位+进度一起筛选，验证岗位一的待索要进度不会串到岗位二的已下载。
	result, err := store.ListPositionCandidates(tenant.ID, PositionCandidateQuery{UserEmail: email, PositionID: p1.ID, Status: "resume_pending", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ResumeState != "pending" || result.Items[0].PositionID != p1.ID {
		t.Fatalf("岗位一待索要筛选不正确：%+v", result)
	}
	if p2Result, err := store.ListPositionCandidates(tenant.ID, PositionCandidateQuery{UserEmail: email, PositionID: p2.ID, Status: "resume_downloaded", Page: 1, PageSize: 20}); err != nil {
		t.Fatal(err)
	} else if p2Result.Total != 1 || p2Result.Items[0].ResumeState != "downloaded" || p2Result.Items[0].PositionID != p2.ID {
		t.Fatalf("岗位二已下载筛选不正确：%+v", p2Result)
	}
	detail, err := store.GetPositionCandidate(tenant.ID, profile.ID, result.Items[0].EngagementID, email, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.RawText != "已保存的完整简历正文" {
		t.Fatalf("跟踪同步覆盖了简历正文：%q", detail.RawText)
	}
	if detail.AIDetailScore == nil || *detail.AIDetailScore != 85 {
		t.Fatalf("评分摘要未入库：%v", detail.AIDetailScore)
	}
	if detail.ResumeRequestedAt != nil {
		t.Fatal("仅待索要，不能标记已索要时间")
	}
	resumeEvents := 0
	for _, event := range detail.Events {
		if event.EventType == "resume_pending" {
			resumeEvents++
		}
	}
	if resumeEvents != 1 {
		t.Fatalf("并发写入产生重复事件：resume_pending=%d", resumeEvents)
	}

	// 推进岗位一到已索要，验证 resume_requested_at 只在 requested 阶段写入。
	item.State, item.UpdatedAt = "requested", at.Add(2*time.Second)
	if err := store.SaveResumeTracking(p1, item); err != nil {
		t.Fatal(err)
	}
	detail2, err := store.GetPositionCandidate(tenant.ID, profile.ID, result.Items[0].EngagementID, email, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail2.ResumeState != "requested" || detail2.ResumeRequestedAt == nil {
		t.Fatalf("已索要进度或时间未更新：state=%q at=%v", detail2.ResumeState, detail2.ResumeRequestedAt)
	}
	// 迟到的旧 pending 报告不能把已索要进度回退。
	item.State, item.UpdatedAt = "pending", at.Add(-time.Second)
	if err := store.SaveResumeTracking(p1, item); err != nil {
		t.Fatal(err)
	}
	detail3, err := store.GetPositionCandidate(tenant.ID, profile.ID, result.Items[0].EngagementID, email, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail3.ResumeState != "requested" {
		t.Fatalf("迟到报告回退了进度：%q", detail3.ResumeState)
	}
}
