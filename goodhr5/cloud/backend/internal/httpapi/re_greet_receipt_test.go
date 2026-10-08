// 本文件验证 HRPlus 复打收据的原编号重试、换编号基准冲突和 PostgreSQL 事务回滚。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

// TestReGreetReceiptHTTP 验证实际路由复用任务归属和设备校验，旧客户端入口仍兼容。
func TestReGreetReceiptHTTP(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "receipt-http@example.com"
	token := loginForTest(t, routes, email)
	bindPositionDeviceForTest(t, routes, token)
	positionID := createPositionWithConfigForTest(t, routes, token, "复打收据", `{"mode_default":"keyword"}`)
	service := server.positionExecution
	tenantID, _ := service.getTenantInfo(email)
	run, err := service.runStore.CreateTaskRun(TaskRun{TenantID: tenantID, UserEmail: email, PositionID: positionID, MachineID: positionTestMachineID, TaskType: "re_greet"})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.screeningStore.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "candidate", SetGreetedAt: true}, positionID)
	if err != nil {
		t.Fatal(err)
	}
	request := reportReGreetRequest{MachineID: positionTestMachineID, OperationID: "http-operation", Platform: "boss", PlatformCandidateID: "candidate", Success: true, RunID: run.ID, BaseContactAt: *candidate.GreetedAt, SentAt: time.Now(), MessageText: "复打消息"}
	post := func(in reportReGreetRequest) int {
		raw, _ := json.Marshal(in)
		return postPositionExecutionForTest(t, routes, token, "/api/positions/"+positionID+"/re-greet-report", string(raw)).Code
	}
	bad := request
	bad.MachineID = "wrong-device"
	if code := post(bad); code != http.StatusForbidden {
		t.Fatalf("错误设备未被拒绝 %d", code)
	}
	for i := 0; i < 3; i++ {
		if code := post(request); code != http.StatusOK {
			t.Fatalf("原编号重复补传失败 %d", code)
		}
	}
	request.MessageText = "改变正文"
	if code := post(request); code != http.StatusConflict {
		t.Fatalf("同编号不同内容未冲突 %d", code)
	}
	current, err := service.screeningStore.FindScreening(t.Context(), positionID, "boss", "candidate")
	if err != nil || current.ReGreetCount != 1 {
		t.Fatalf("重复记账 %+v %v", current, err)
	}
}

// TestReGreetReceiptMemory 验证开发存储也不会因并发补传重复增加次数。
func TestReGreetReceiptMemory(t *testing.T) {
	store := NewMemoryCandidateScreeningStore()
	item, err := store.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "candidate", SetGreetedAt: true}, "position")
	if err != nil {
		t.Fatal(err)
	}
	input := ReGreetReceiptInput{OperationID: "operation", OwnerEmail: "owner@example.com", PositionID: "position", Platform: "boss", CandidateID: "candidate", BaseContactAt: *item.GreetedAt, SentAt: time.Now().UTC(), MessageText: "再次沟通"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := store.CommitReGreetReceipt(t.Context(), input)
			if err != nil || receipt.ResultCount != 1 {
				t.Errorf("重复计数: %+v %v", receipt, err)
			}
		}()
	}
	wg.Wait()
	conflict := input
	conflict.OperationID = "another"
	if _, err = store.CommitReGreetReceipt(t.Context(), conflict); !errors.Is(err, ErrReGreetReceiptConflict) {
		t.Fatalf("换编号基准应冲突: %v", err)
	}
	conflict = input
	conflict.OwnerEmail = "another@example.com"
	if _, err = store.CommitReGreetReceipt(t.Context(), conflict); !errors.Is(err, ErrReGreetReceiptConflict) {
		t.Fatalf("跨账号复用编号应冲突: %v", err)
	}
}

// TestReGreetReceiptPostgres 只连接显式独立测试 DSN，使用实际迁移与真实并发事务。
func TestReGreetReceiptPostgres(t *testing.T) {
	dsn := os.Getenv("GOODHR_M1_RECEIPT_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未配置独立 M1 收据测试数据库")
	}
	t.Chdir("../..")
	db, err := (Config{PostgresDSN: dsn}).PostgresDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	email := "receipt-" + time.Now().Format("150405.000000000") + "@example.com"
	if _, err = NewPostgresTenantStore(db).GetOrCreateTenant(email); err != nil {
		t.Fatal(err)
	}
	position, err := NewPostgresPositionStore(db).SavePosition(Position{UserEmail: email, PlatformID: "boss", Name: "M1 独立测试"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCandidateScreeningStore(db)
	item, err := store.UpsertScreening(t.Context(), CandidateScreeningUpsert{Platform: "boss", PlatformCandidateID: "candidate", SetGreetedAt: true}, position.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewPostgresCandidateStore(db).SaveCandidateProfile(CandidateProfileInput{UserEmail: email, PlatformID: "boss", PlatformCandidateID: "candidate", CandidateName: "同名"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewPostgresCandidateStore(db).UpsertCandidateEngagement(CandidateEngagement{CandidateID: profile.ID, PositionID: position.ID, PlatformID: "boss", UserEmail: email}); err != nil {
		t.Fatal(err)
	}
	input := ReGreetReceiptInput{OperationID: "receipt-" + position.ID, OwnerEmail: email, PositionID: position.ID, Platform: "boss", CandidateID: "candidate", BaseContactAt: *item.GreetedAt, SentAt: time.Now().UTC().Truncate(time.Microsecond), MessageText: "再次沟通"}
	// 必需事件写入失败发生在收据和次数更新之后，验证全部回滚。
	broken := input
	broken.RunID = "not-a-uuid"
	if _, err = store.CommitReGreetReceipt(t.Context(), broken); err == nil {
		t.Fatal("事件写入应失败")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM re_greet_receipts WHERE operation_id=$1`, input.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("收据未回滚 %d %v", count, err)
	}
	current, err := store.FindScreening(t.Context(), position.ID, "boss", "candidate")
	if err != nil || current.ReGreetCount != 0 {
		t.Fatalf("次数未回滚 %+v %v", current, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := store.CommitReGreetReceipt(t.Context(), input)
			if err != nil || receipt.ResultCount != 1 {
				t.Errorf("并发结果错误 %+v %v", receipt, err)
			}
		}()
	}
	wg.Wait()
	// 原编号丢回执再试必须返回原结果；同编号改变正文、换编号重复基准均冲突。
	receipt, err := store.CommitReGreetReceipt(t.Context(), input)
	if err != nil || !receipt.SentAt.Equal(input.SentAt) {
		t.Fatalf("重试未返回原收据 %+v %v", receipt, err)
	}
	conflict := input
	conflict.MessageText = "其他正文"
	if _, err = store.CommitReGreetReceipt(t.Context(), conflict); !errors.Is(err, ErrReGreetReceiptConflict) {
		t.Fatalf("正文变化应冲突 %v", err)
	}
	conflict = input
	conflict.OperationID += "-new"
	if _, err = store.CommitReGreetReceipt(t.Context(), conflict); !errors.Is(err, ErrReGreetReceiptConflict) {
		t.Fatalf("基准重用应冲突 %v", err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM candidate_events WHERE position_id=$1 AND event_type='re_greeted_sent'`, position.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("事件重复 %d %v", count, err)
	}
	current, err = store.FindScreening(t.Context(), position.ID, "boss", "candidate")
	if err != nil || current.ReGreetCount != 1 || !current.LastReGreetedAt.Equal(input.SentAt) {
		t.Fatalf("事实错误 %+v %v", current, err)
	}
}
