// 本文件验证 HRPlus 只读状态推送的认证、断线重读、账号隔离、撤销登录和数据库提交边界。
package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// TestPlanEventHubIsolation 验证慢订阅合并通知、账号隔离及退出后的资源释放。
func TestPlanEventHubIsolation(t *testing.T) {
	hub := &planEventHub{}
	a, closeA, ok := hub.subscribe("A")
	if !ok {
		t.Fatal("订阅失败")
	}
	defer closeA()
	b, closeB, _ := hub.subscribe("B")
	defer closeB()
	for range 100 {
		hub.publish("A")
	}
	if len(a) != 1 || len(b) != 0 {
		t.Fatal("未合并提示或跨账号推送")
	}
	closes := []func(){}
	for range 7 {
		_, close, ok := hub.subscribe("A")
		if !ok {
			t.Fatal("正常订阅失败")
		}
		closes = append(closes, close)
	}
	if _, _, ok := hub.subscribe("A"); ok {
		t.Fatal("未限制同账号连接数")
	}
	for _, close := range closes {
		close()
	}
	closeA()
	if _, close, ok := hub.subscribe("A"); !ok {
		t.Fatal("取消后未释放连接额度")
	} else {
		close()
	}
}

// planEventClient 使用真实 HTTP 流读取完整事件，不把未完成的分片当状态改变。
func planEventClient(t *testing.T, url, token string) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		cancel()
		response.Body.Close()
		t.Fatal("订阅失败", response.StatusCode)
	}
	events := make(chan string, 16)
	go func() {
		defer close(events)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		event := ""
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if line == "" && event != "" {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
				event = ""
			}
		}
	}()
	return events, cancel
}

// awaitPlanEvent 等待指定事件或明确关闭，失败时不给旧连接生成新的处理流程。
func awaitPlanEvent(t *testing.T, events <-chan string, expected string) {
	t.Helper()
	select {
	case event, open := <-events:
		if !open || event != expected {
			t.Fatal("推送事件不符", event, open, expected)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到主动通知", expected)
	}
}

// TestPlanEventHTTPMemory 验证实际停止请求主动通知两个网页，重连 ready 且撤销令牌结束原连接。
func TestPlanEventHTTPMemory(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "plan-events@example.com"
	token := loginForTest(t, routes, email)
	tenant, err := server.auth.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := server.executionPlans.store.Save(t.Context(), ExecutionPlan{TenantID: tenant.ID, UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = server.executionPlans.store.Intent(t.Context(), tenant.ID, email, plan.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(routes)
	defer httpServer.Close()
	a, closeA := planEventClient(t, httpServer.URL+"/api/execution-plan-events", token)
	defer closeA()
	b, closeB := planEventClient(t, httpServer.URL+"/api/execution-plan-events", token)
	defer closeB()
	awaitPlanEvent(t, a, "ready")
	awaitPlanEvent(t, b, "ready")
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = plan.ActivationID
	raw, _ := json.Marshal(stop)
	req := httptest.NewRequest(http.MethodPost, "/api/execution-plans/"+plan.ID+"/stop", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, req)
	if recorder.Code != 200 {
		t.Fatal("停止请求失败", recorder.Body.String())
	}
	awaitPlanEvent(t, a, "changed")
	awaitPlanEvent(t, b, "changed")
	closeA()
	reconnected, closeAgain := planEventClient(t, httpServer.URL+"/api/execution-plan-events", token)
	defer closeAgain()
	awaitPlanEvent(t, reconnected, "ready")
	if err := server.auth.store.(SessionRevoker).RevokeSession(token); err != nil {
		t.Fatal(err)
	}
	server.executionPlans.events.publish(planEventScope(tenant.ID, email))
	select {
	case _, open := <-reconnected:
		if open {
			t.Fatal("失效登录继续推送")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("失效订阅未退出")
	}
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/execution-plan-events?token="+token, nil))
	if response.Code != 401 {
		t.Fatal("查询字符串凭证被接受")
	}
}

// TestPlanEventReadAndFailure 验证普通读取和被拒绝的修改不发送成功变更提示。
func TestPlanEventReadAndFailure(t *testing.T) {
	server := mustNewServer(t)
	email := "events-rejected@example.com"
	token := loginForTest(t, server.Routes(), email)
	tenant, err := server.auth.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	changes, close, ok := server.executionPlans.events.subscribe(planEventScope(tenant.ID, email))
	if !ok {
		t.Fatal("订阅失败")
	}
	defer close()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/execution-plans", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.Routes().ServeHTTP(response, req)
		if method == http.MethodGet && response.Code != 200 {
			t.Fatal("读取失败")
		}
		if method == http.MethodPost && response.Code < 400 {
			t.Fatal("空修改被接受")
		}
		if len(changes) != 0 {
			t.Fatal("读取或失败修改发出成功变更提示")
		}
	}
}

// TestPlanReportEventPostgres 验证原报告和通知结果也是提交后推送，不需要重新启动计划。
func TestPlanReportEventPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	store := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	email += "@example.com"
	_, summary := endedReportFixture(t, store, email)
	listener := pq.NewListener(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"), time.Second, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("hrplus_plan_changes"); err != nil {
		t.Fatal(err)
	}
	scope := planEventScope("", email)
	check := func() {
		t.Helper()
		select {
		case message := <-listener.Notify:
			if message == nil || message.Extra != scope {
				t.Fatal("报告通知作用域错误", message)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("报告更新没有通知")
		}
	}
	if _, err := store.SaveReport(t.Context(), "", email, summary, "confirmed"); err != nil {
		t.Fatal(err)
	}
	check()
	notificationID, _ := newExecutionPlanID()
	report, claimed, err := store.ClaimReportNotification(t.Context(), "", email, summary.RunID, notificationID)
	if err != nil || !claimed {
		t.Fatal("领取通知失败", err)
	}
	check()
	if err := store.FinishReportNotification(t.Context(), "", email, summary.RunID, report.NotificationToken, "sent", ""); err != nil {
		t.Fatal(err)
	}
	check()
}

// TestPlanEventPostgresCommit 验证不同连接收到提交后的作用域，未提交/回滚不推送且不包含业务明文。
func TestPlanEventPostgresCommit(t *testing.T) {
	db := planPostgresFixture(t)
	listener := pq.NewListener(os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN"), time.Second, time.Second, nil)
	defer listener.Close()
	if err := listener.Listen("hrplus_plan_changes"); err != nil {
		t.Fatal(err)
	}
	email, _ := newExecutionPlanID()
	email += "@example.com"
	plan := createArmedPlanFixture(t, NewPostgresExecutionPlanStore(db), email)
	scope := planEventScope("", email)
	select {
	case message := <-listener.Notify:
		if message == nil || message.Extra != scope {
			t.Fatal("作用域不符或泄露明文", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("没有数据库通知")
	}
	// 排空创建及启用两个事务的提示，之后用事务内通知证明提交边界。
	if err := listener.UnlistenAll(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Listen("hrplus_plan_changes"); err != nil {
		t.Fatal(err)
	}
	for len(listener.Notify) > 0 {
		<-listener.Notify
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE execution_plans SET state_sequence=state_sequence+1 WHERE id=$1`, plan.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	select {
	case message := <-listener.Notify:
		t.Fatal("未提交即推送", message)
	case <-time.After(80 * time.Millisecond):
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-listener.Notify:
		t.Fatal("回滚仍推送", message)
	case <-time.After(80 * time.Millisecond):
	}
	if _, err = db.Exec(`UPDATE execution_plans SET state_sequence=state_sequence+1 WHERE id=$1`, plan.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-listener.Notify:
		if message == nil || message.Extra != scope {
			t.Fatal("提交后作用域不符", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("提交后没有通知")
	}
}

// TestPlanEventCrossServerPostgres 验证两个独立云端服务连接收到同一提交，另一账号没有变更提示。
func TestPlanEventCrossServerPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	first := mustNewServer(t)
	first.auth.tenantStore = NewPostgresTenantStore(db)
	email := "cross-" + time.Now().Format("150405.000000000") + "@example.com"
	token := loginForTest(t, first.Routes(), email)
	otherToken := loginForTest(t, first.Routes(), "other-events@example.com")
	tenant, err := first.auth.tenantStore.GetOrCreateTenant(email)
	if err != nil {
		t.Fatal(err)
	}
	second := NewExecutionPlanService(first.positions, first.executionPlans.agents, NewPostgresExecutionPlanStore(db))
	first.executionPlans.eventDSN = os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN")
	second.eventDSN = first.executionPlans.eventDSN
	aServer := httptest.NewServer(http.HandlerFunc(first.executionPlans.Events))
	defer aServer.Close()
	bServer := httptest.NewServer(http.HandlerFunc(second.Events))
	defer bServer.Close()
	a, closeA := planEventClient(t, aServer.URL, token)
	defer closeA()
	b, closeB := planEventClient(t, bServer.URL, token)
	defer closeB()
	other, closeOther := planEventClient(t, bServer.URL, otherToken)
	defer closeOther()
	awaitPlanEvent(t, a, "ready")
	awaitPlanEvent(t, b, "ready")
	awaitPlanEvent(t, other, "ready")
	store := NewPostgresExecutionPlanStore(db)
	plan, err := store.Save(t.Context(), ExecutionPlan{TenantID: tenant.ID, UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	awaitPlanEvent(t, a, "changed")
	awaitPlanEvent(t, b, "changed")
	select {
	case event := <-other:
		t.Fatal("另一账号收到变更", event)
	case <-time.After(80 * time.Millisecond):
	}
	plan, err = store.Intent(t.Context(), tenant.ID, email, plan.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	awaitPlanEvent(t, a, "changed")
	awaitPlanEvent(t, b, "changed")
	claim := planRunClaimFixture(t, plan)
	permit, err := store.ClaimRun(t.Context(), tenant.ID, email, claim)
	if err != nil {
		t.Fatal(err)
	}
	awaitPlanEvent(t, a, "changed")
	awaitPlanEvent(t, b, "changed")
	if _, err = db.Exec(`UPDATE execution_plan_item_runs SET counts=counts WHERE run_id=$1`, permit.Run.ID); err != nil {
		t.Fatal(err)
	}
	awaitPlanEvent(t, a, "changed")
	awaitPlanEvent(t, b, "changed")
	run, err := store.GetRun(t.Context(), tenant.ID, email, permit.Run.ID)
	if err != nil || run.State != "starting" || run.Sequence != 1 {
		t.Fatal("订阅修改或启动任务", run, err)
	}
}
