// 本文件验证 HRPlus 计划运行领取的真实事务、账号冲突、跨窗口游标及原请求重试。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// planRunClaimFixture 生成本地已预留的领取请求，凭证仅用于测试。
func planRunClaimFixture(t *testing.T, p ExecutionPlan) ExecutionPlanRunClaim {
	t.Helper()
	run, _ := newExecutionPlanID()
	request, _ := newExecutionPlanID()
	owner, _ := newExecutionPlanID()
	return ExecutionPlanRunClaim{PlanID: p.ID, ActivationID: p.ActivationID, ExpectedVersion: p.Version, ExecutionDate: "2026-10-10", RunID: run, RequestID: request, MachineID: p.MachineID, OwnerID: owner, Credential: strings.Repeat("fixture-credential-", 3), LocalReserved: true}
}

// createArmedPlanFixture 保存并启用一个独立计划，测试不调用招聘页面。
func createArmedPlanFixture(t *testing.T, s ExecutionPlanStore, email string) ExecutionPlan {
	t.Helper()
	p, err := s.Save(t.Context(), ExecutionPlan{UserEmail: email, MachineID: "A", Config: postgresPlanConfig()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Intent(t.Context(), "", email, p.ID, planIntentFixture(t, "arm"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// testPlanRunClaimContract 验证相同请求只保留一个 starting，停止后不再返回可启动许可。
func testPlanRunClaimContract(t *testing.T, s ExecutionPlanStore) {
	t.Helper()
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	bad := c
	bad.LocalReserved = false
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, bad); err == nil {
		t.Fatal("无本地预留许可")
	}
	result, err := s.ClaimRun(t.Context(), "", p.UserEmail, c)
	if err != nil || result.Run.ID != c.RunID || result.Run.State != "starting" || result.Owner.State != "starting" || result.Run.CurrentItem != 0 {
		t.Fatal("领取提前运行或编号不符", result, err)
	}
	result.Run.Snapshot.Items[0].Actions[0] = "fake"
	replay, err := s.ClaimRun(t.Context(), "", p.UserEmail, c)
	if err != nil || replay.Run.ID != c.RunID || replay.Run.Sequence != 1 || replay.Run.Snapshot.Items[0].Actions[0] == "fake" {
		t.Fatal("重试生成新记录或篡改原快照", err)
	}
	raw, _ := json.Marshal(replay)
	if bytes.Contains(raw, []byte(c.Credential)) {
		t.Fatal("许可含凭证原文")
	}
	bad = c
	bad.Credential = strings.Repeat("another", 10)
	if _, err = s.ClaimRun(t.Context(), "", p.UserEmail, bad); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("同编号换凭证", err)
	}
	other := createArmedPlanFixture(t, s, p.UserEmail)
	competing := planRunClaimFixture(t, other)
	if _, err = s.ClaimRun(t.Context(), "", p.UserEmail, competing); !errors.Is(err, ErrAccountExecutionBusy) {
		t.Fatal("第二计划越过占用", err)
	}
	stop := planIntentFixture(t, "stop")
	stop.ActivationID = p.ActivationID
	if _, err = s.Intent(t.Context(), "", p.UserEmail, p.ID, stop); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimRun(t.Context(), "", p.UserEmail, c); !errors.Is(err, ErrExecutionPlanRequest) {
		t.Fatal("停止后重试仍得到启动许可", err)
	}
}

// TestPlanRunClaimStores 分别验证内存和独立 PostgreSQL 的相同运行领取契约。
func TestPlanRunClaimStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testPlanRunClaimContract(t, NewMemoryExecutionPlanStore()) })
	t.Run("postgres", func(t *testing.T) { testPlanRunClaimContract(t, NewPostgresExecutionPlanStore(planPostgresFixture(t))) })
}

// TestPlanRunClaimResumePostgres 验证同日重新领取保留运行与游标，次日创建第一项的新运行。
func TestPlanRunClaimResumePostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE execution_plan_runs SET state='waiting_window',current_item=1 WHERE id=$1`, c.RunID); err != nil {
		t.Fatal(err)
	}
	release := c.accountClaim(p.UserEmail)
	release.RequestID, _ = newExecutionPlanID()
	if err := NewPostgresAccountExecutionStore(db).Release(t.Context(), release, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimRun(t.Context(), "", p.UserEmail, c); !errors.Is(err, ErrAccountExecutionReleased) {
		t.Fatal("旧许可重新领取", err)
	}
	next := planRunClaimFixture(t, p)
	next.RunID = c.RunID
	resumed, err := s.ClaimRun(t.Context(), "", p.UserEmail, next)
	if err != nil || resumed.Run.CurrentItem != 1 || resumed.Run.ID != c.RunID || resumed.Run.Sequence != 2 {
		t.Fatal("同日进度丢失", err)
	}
	if _, err = db.Exec(`UPDATE execution_plan_runs SET state='incomplete' WHERE id=$1`, c.RunID); err != nil {
		t.Fatal(err)
	}
	release = next.accountClaim(p.UserEmail)
	release.RequestID, _ = newExecutionPlanID()
	if err = NewPostgresAccountExecutionStore(db).Release(t.Context(), release, true); err != nil {
		t.Fatal(err)
	}
	tomorrow := planRunClaimFixture(t, p)
	tomorrow.ExecutionDate = "2026-10-11"
	fresh, err := s.ClaimRun(t.Context(), "", p.UserEmail, tomorrow)
	if err != nil || fresh.Run.CurrentItem != 0 || fresh.Run.ID == c.RunID {
		t.Fatal("次日未从第一项开始", err)
	}
}

// TestPlanRunClaimRollbackPostgres 验证运行写入冲突时，刚申请的账号占用与回执全部回滚。
func TestPlanRunClaimRollbackPostgres(t *testing.T) {
	db := planPostgresFixture(t)
	s := NewPostgresExecutionPlanStore(db)
	email, _ := newExecutionPlanID()
	p := createArmedPlanFixture(t, s, email+"@example.com")
	c := planRunClaimFixture(t, p)
	_, err := db.Exec(`INSERT INTO execution_plan_runs(id,plan_id,activation_id,execution_date,config_version,snapshot,state) VALUES($1,$2,$3,'2026-10-09',1,'{}','completed')`, c.RunID, c.PlanID, c.ActivationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimRun(t.Context(), "", p.UserEmail, c); err == nil {
		t.Fatal("运行编号冲突仍许可")
	}
	var count int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM account_execution_owners WHERE account_key=$1)+(SELECT count(*) FROM account_execution_requests WHERE account_key=$1)`, p.UserEmail).Scan(&count); err != nil || count != 0 {
		t.Fatal("失败领取遗留占用或回执", count, err)
	}
}

// TestPlanRunLegacyMemoryCompetition 验证内存计划领取与旧手动启动共用岗位锁。
func TestPlanRunLegacyMemoryCompetition(t *testing.T) {
	positions := NewMemoryPositionStore()
	plans := NewMemoryExecutionPlanStore()
	plans.positions = positions
	email := "shared@example.com"
	position, err := positions.SavePosition(Position{UserEmail: email, Name: "fixture", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	p := createArmedPlanFixture(t, plans, email)
	c := planRunClaimFixture(t, p)
	barrier := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-barrier; results <- positions.ClaimPositionStart(email, position.ID) }()
	go func() { <-barrier; _, e := plans.ClaimRun(t.Context(), "", email, c); results <- e }()
	close(barrier)
	successful := 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			successful++
		} else if !errors.Is(e, ErrPositionAlreadyRunning) && !errors.Is(e, ErrAccountExecutionBusy) {
			t.Fatal(e)
		}
	}
	if successful != 1 {
		t.Fatalf("旧手动与计划同时启动: %d", successful)
	}
}

// TestPlanRunLegacyPostgresCompetition 验证真实 SQL 中两个计划和旧手动入口同刻竞争只有一个获准。
func TestPlanRunLegacyPostgresCompetition(t *testing.T) {
	db := planPostgresFixture(t)
	plans := NewPostgresExecutionPlanStore(db)
	positions := NewPostgresPositionStore(db)
	email, _ := newExecutionPlanID()
	email += "@example.com"
	position, err := positions.SavePosition(Position{UserEmail: email, Name: "fixture", PlatformID: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	first := createArmedPlanFixture(t, plans, email)
	second := createArmedPlanFixture(t, plans, email)
	barrier := make(chan struct{})
	results := make(chan error, 3)
	for _, p := range []ExecutionPlan{first, second} {
		c := planRunClaimFixture(t, p)
		go func() { <-barrier; _, e := plans.ClaimRun(t.Context(), "", email, c); results <- e }()
	}
	go func() { <-barrier; results <- positions.ClaimPositionStart(email, position.ID) }()
	close(barrier)
	successes := 0
	for i := 0; i < 3; i++ {
		e := <-results
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrAccountExecutionBusy) && !errors.Is(e, ErrPositionAlreadyRunning) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatalf("计划及旧手动同时许可: %d", successes)
	}
	var occupied, premature int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM account_execution_owners WHERE account_key=$1)+(SELECT count(*) FROM positions p JOIN users u ON u.id=p.user_id WHERE u.email=$1 AND p.status='running')`, email).Scan(&occupied); err != nil || occupied != 1 {
		t.Fatal("账号实际占用数不唯一", occupied, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM execution_plan_runs WHERE plan_id IN ($1,$2) AND (state<>'starting' OR started_at IS NOT NULL)`, first.ID, second.ID).Scan(&premature); err != nil || premature != 0 {
		t.Fatal("计划提前写实际开始", premature, err)
	}
}

// TestPlanRunClaimAPI 验证公开领取使用真实会话、指定设备，并拒绝伪造字段及重复执行。
func TestPlanRunClaimAPI(t *testing.T) {
	server := mustNewServer(t)
	routes := server.Routes()
	email := "plan-run-api@example.com"
	token := loginForTest(t, routes, email)
	bindPositionDeviceForTest(t, routes, token)
	if err := server.positions.systemConfigs.Save(SystemConfig{ConfigKey: "platform.boss", ConfigValue: `{"open":true}`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	position := createPositionWithConfigForTest(t, routes, token, "运行岗位", `{"mode_default":"keyword"}`)
	config := validPlanConfig()
	for i := range config.Items {
		config.Items[i].PositionID = position
		config.Items[i].Actions = []string{"greeting"}
		config.Items[i].PrioritizeReply = false
	}
	post := func(path string, value any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(value)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		routes.ServeHTTP(res, req)
		return res
	}
	saved := post("/api/execution-plans", map[string]any{"machine_id": positionTestMachineID, "expected_version": 0, "config": config})
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	var body struct {
		Plan ExecutionPlan `json:"plan"`
	}
	_ = json.Unmarshal(saved.Body.Bytes(), &body)
	arm := post("/api/execution-plans/"+body.Plan.ID+"/arm", planIntentFixture(t, "arm"))
	if arm.Code != 200 {
		t.Fatal(arm.Body.String())
	}
	_ = json.Unmarshal(arm.Body.Bytes(), &body)
	input := planRunClaimFixture(t, body.Plan)
	rawInput, _ := json.Marshal(input)
	var forged map[string]any
	_ = json.Unmarshal(rawInput, &forged)
	forged["user_email"] = "foreign@example.com"
	if res := post("/api/execution-plan-runs/claim", forged); res.Code != 400 {
		t.Fatal("客户端伪造账号字段被接受", res.Body.String())
	}
	input.MachineID = "wrong"
	if res := post("/api/execution-plan-runs/claim", input); res.Code != 403 {
		t.Fatal("其他电脑领取", res.Body.String())
	}
	input.MachineID = positionTestMachineID
	if res := post("/api/execution-plan-runs/claim", input); res.Code != 200 || strings.Contains(res.Body.String(), input.Credential) {
		t.Fatal("实际领取失败或凭证回显", res.Body.String())
	}
	if res := post("/api/execution-plan-runs/claim", input); res.Code != 200 {
		t.Fatal("断线重试未返回原许可", res.Body.String())
	}
	other := planRunClaimFixture(t, body.Plan)
	if res := post("/api/execution-plan-runs/claim", other); res.Code != 409 {
		t.Fatal("重复执行通过", res.Body.String())
	}
}
