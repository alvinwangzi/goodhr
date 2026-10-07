// 本文件在独立测试数据库验证页面观察的实际 PostgreSQL 读写，防止未知时间、评分覆盖和简历状态回退。
package httpapi

import (
	"os"
	"testing"
	"time"
)

// TestCandidatePageObservationPostgres 使用显式测试 DSN 验证真实 SQL，默认不接触任何业务数据库。
func TestCandidatePageObservationPostgres(t *testing.T) {
	dsn := os.Getenv("GOODHR_SCREENING_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未配置独立候选人状态测试数据库")
	}
	t.Chdir("../..")
	db, err := (Config{PostgresDSN: dsn}).PostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	email := "observation-" + time.Now().Format("150405.000000000") + "@example.com"
	if _, err := NewPostgresTenantStore(db).GetOrCreateTenant(email); err != nil {
		t.Fatal(err)
	}
	position, err := NewPostgresPositionStore(db).SavePosition(Position{UserEmail: email, PlatformID: "boss", Name: "页面核对测试"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCandidateScreeningStore(db)
	_, err = store.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "manual", CandidateName: "同名", Score: 88, Status: "passed", Source: "greeting"}, position.ID)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "manual", CandidateName: "同名", ResumeStatus: "received", ContactObserved: true, Source: "platform_observation"}, position.ID)
	if err != nil {
		t.Fatal(err)
	}
	if observed.GreetedAt != nil || observed.PlatformObservedAt == nil || !observed.ContactObserved || observed.Score != 88 {
		t.Fatalf("真实 SQL 伪造时间或覆盖资料：%+v", observed)
	}
	_, err = store.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "manual", CandidateName: "同名", ResumeStatus: "requested", Source: "auto_reply"}, position.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.FindScreening(t.Context(), position.ID, "boss", "manual")
	if err != nil || current.ResumeStatus != "received" || !current.ContactObserved {
		t.Fatalf("真实 SQL 状态回退：%+v %v", current, err)
	}
	byName, err := store.FindScreeningByName(t.Context(), position.ID, "boss", "同名")
	if err != nil || byName.PlatformCandidateID != "manual" {
		t.Fatalf("查询失败：%+v %v", byName, err)
	}
	if items, _, err := store.ListScreeningsByPosition(t.Context(), position.ID, 20, 0); err != nil || len(items) != 1 {
		t.Fatalf("列表 SQL 失败：%+v %v", items, err)
	}
	if items, err := store.ListReGreetCandidates(t.Context(), position.ID, "boss", 7, 30, 1); err != nil || len(items) != 0 {
		t.Fatalf("未确定实际招呼时间的记录进入复打：%+v %v", items, err)
	}
}
