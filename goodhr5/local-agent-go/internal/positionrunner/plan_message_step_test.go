// 本文件用受控平台事实验证 HRPlus 计划消息有限批次、原归属、计数保留及未激活动作保护。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/platformcore"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// planMessageFixture 复用真实 M1 回复编排和 SQLite，仅替换招聘页面及发送边界。
func planMessageFixture(t *testing.T, count int, configure ...func(*planmodel.Permit)) (*PlanItemReservation, planmodel.Permit, PositionRuntimeSnapshot, *cooperativeFixture) {
	t.Helper()
	session, page, _ := cooperativeSessionFixture(t, true, count)
	r := session.runner
	r.clear(session.position.ID)
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err = json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	for i := range permit.Run.Snapshot.Items {
		permit.Run.Snapshot.Items[i].PositionID = session.position.ID
		permit.Run.Items[i].Snapshot = permit.Run.Snapshot.Items[i]
	}
	permit.Run.Snapshot.Items[0].Actions = []string{"auto_reply"}
	permit.Run.Items[0].Snapshot = permit.Run.Snapshot.Items[0]
	permit.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"auto_reply": {State: "active", Count: 5}}
	permit.Run.Items[0].State = "running"
	permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	permit.Run.State, permit.Owner.State = "running", "running"
	started := time.Now().UTC()
	permit.Run.StartedAt = &started
	for _, apply := range configure {
		apply(&permit)
	}
	taskType := strings.Join(permit.Run.Items[0].Snapshot.Actions, ",")
	priority := permit.Run.Items[0].Snapshot.PrioritizeReply
	checkpoint, err := r.db.EnsurePlanActionRun(t.Context(), localdb.ActionCheckpoint{PlanRunID: permit.Run.ID, ItemRunID: permit.Run.Items[0].ID, OwnerScope: "fixture-owner", PositionID: session.position.ID, Platform: "boss", ProfileScope: session.flow.scope, CloudRunID: permit.Run.Items[0].TaskRunID, TaskType: taskType, PrioritizeReply: priority, Greeted: 7, Replied: 5, ReplyStats: map[string]int{"checked": 5, "replied": 5}, PositionSnapshot: session.position.PositionSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.ReservePlanBrowser(t.Context(), permit.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{CloudRunID: checkpoint.CloudRunID, TaskType: taskType, PrioritizeReply: priority, MachineID: permit.Owner.MachineID}
	child, err := parent.BorrowItem(checkpoint.OwnerScope, checkpoint.ItemRunID, options)
	if err != nil {
		t.Fatal(err)
	}
	child.ownerID = permit.Owner.OwnerID
	session.options = child.state.options
	session.flow.runID = checkpoint.CloudRunID
	session.onRecommendation = false
	session.scheduler = actiondispatch.New(true)
	child.messages = session
	snapshot := PositionRuntimeSnapshot{Position: session.position, Options: child.state.options}
	t.Cleanup(func() { _ = child.ReleaseAfterCleanup(true); _ = parent.Release(true) })
	return child, permit, snapshot, page
}

// TestPlanChildDoesNotUseStandaloneSettlement 验证子动作结束和失败不调用独立状态或通知入口。
func TestPlanChildDoesNotUseStandaloneSettlement(t *testing.T) {
	child, _, _, _ := planMessageFixture(t, 0)
	var calls atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer cloud.Close()
	options := child.state.options
	options.Token, options.CloudAPIBase, options.EmailForNotify = "fixture-token", cloud.URL, "fixture@example.com"
	r := child.parent.runner
	r.notifyCloudPositionStopped(child.positionID, options)
	r.notifyCloudPositionCompleted(child.positionID, options)
	r.notifyCloudAutoReplyStatus(child.positionID, options, "completed", platformcore.ReplyStats{})
	r.notifyCloudReGreetStatus(child.positionID, options, "stopped", reGreetStats{})
	r.sendPositionFailNotification(t.Context(), child.positionID, "fixture failure", options)
	r.failStart(child.positionID, "fixture failure", options)
	if calls.Load() != 0 || !child.parent.Valid() || child.state.planActionError == nil {
		t.Fatal("子失败独立结算或通知，或丢失失败事实")
	}
}

// TestPlanReGreetStepFiniteBatch 验证到期复打独立三人批次、原任务收据与未领取名单保留。
func TestPlanReGreetStepFiniteBatch(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 0, func(p *planmodel.Permit) {
		p.Run.Snapshot.Items[0].Actions = []string{"re_greet"}
		p.Run.Snapshot.Items[0].PrioritizeReply = false
		p.Run.Items[0].Snapshot = p.Run.Snapshot.Items[0]
		p.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"re_greet": {State: "active"}}
	})
	s := child.messages
	f := &cooperativeReGreetFixture{cooperativeFixture: page, reGreet: &reGreetFixture{replyFixture: page.replyFixture, confirmed: true, current: platformcore.ReplyContext{ResumeStatus: "none", Messages: []platformcore.ReplyMessage{{Direction: "outbound", Kind: "text", Text: "首次招呼"}}}}}
	s.runtime, s.flow.runtime, s.flow.generator = f, f, f
	basis := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339Nano)
	candidates := []map[string]any{}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("plan-geek-%d", i)
		candidates = append(candidates, map[string]any{"platform_candidate_id": id, "candidate_name": "同名", "greeted_at": basis, "re_greet_count": 0})
		if err := s.runner.db.SaveCandidateIdentity(t.Context(), localdb.CandidateIdentity{ProfileScope: s.flow.scope, Platform: "boss", RecommendationID: id, ConversationID: "conversation-" + id, Status: "verified", Source: "controlled-direct-transition"}); err != nil {
			t.Fatal(err)
		}
	}
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
		case strings.HasSuffix(r.URL.Path, "/re-greet-candidates"):
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "re_greet_receipts": true, "items": candidates})
		case strings.HasSuffix(r.URL.Path, "/re-greet-report"):
			var request cloudapi.ReGreetReceiptRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.RunID != permit.Run.Items[0].TaskRunID {
				t.Error("复打没有保留原 TaskRun")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: request.OperationID, ResultCount: request.BaseCount + 1, SentAt: request.SentAt, ReceivedAt: time.Now()}})
		case strings.HasSuffix(r.URL.Path, "/status") || strings.Contains(r.URL.Path, "notify"):
			t.Error("计划步骤调用了独立岗位结束或通知", r.URL.Path)
			w.WriteHeader(409)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	defer cloud.Close()
	child.state.options.CloudAPIBase, child.state.options.Token = cloud.URL, "fixture-token"
	s.flow.cloudClient = cloudapi.New(cloud.URL)
	for index, want := range []int{3, 6, 8} {
		step, err := child.MessageStep(permit, snapshot, index == 0)
		if err != nil || f.reGreet.sent != want || step.Action != "re_greet" || step.Remaining != (want < 8) {
			t.Fatal("复打没有按有限批次交还", step, f.reGreet.sent, err)
		}
	}
	step, err := child.MessageStep(permit, snapshot, true)
	if err != nil || !step.NoWork || f.reGreet.sent != 8 {
		t.Fatal("复打无到期数据时没有结束", step, err)
	}
}

// TestPlanMessageStepFiniteBatch 验证八个会话分三批处理，累计值保留且无工作时立即交还主调度。
func TestPlanMessageStepFiniteBatch(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 8)
	for index, want := range []int{3, 6, 8} {
		step, err := child.MessageStep(permit, snapshot, index == 0)
		if err != nil || page.sends != want || step.Action != "auto_reply" || step.NoWork || step.Remaining != (want < 8) || step.TaskRunID != permit.Run.Items[0].TaskRunID {
			t.Fatalf("消息没有按单批交还: step=%+v sends=%d err=%v", step, page.sends, err)
		}
		checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
		if err != nil || checkpoint.Replied != 5+want || checkpoint.Greeted != 7 {
			t.Fatal("消息批次重置累计值或改了主扫描数量", err)
		}
	}
	step, err := child.MessageStep(permit, snapshot, true)
	if err != nil || !step.NoWork || step.Remaining || page.sends != 8 {
		t.Fatal("无当前消息时没有立即结束", step, err)
	}
	if !child.parent.Valid() {
		t.Fatal("消息批次清掉了父占用")
	}
}

// TestPlanMessageStepTimeBudget 验证单人安全单元超过一分钟时，余下名单立即交还而不再领取第二人。
func TestPlanMessageStepTimeBudget(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 8)
	now := time.Now()
	child.messages.now = func() time.Time { return now }
	page.generate = func(context.Context) (string, error) {
		now = now.Add(61 * time.Second)
		return "受控回复", nil
	}
	step, err := child.MessageStep(permit, snapshot, true)
	if err != nil || page.sends != 1 || len(child.messages.replies) != 7 || !step.Remaining {
		t.Fatal("时间预算到期仍领取下一会话或丢名单", step, page.sends, err)
	}
}

// TestPlanMessageUnknownKeepsProtection 验证未确认发送不计成功，再检查同一入站消息不会重复发送。
func TestPlanMessageUnknownKeepsProtection(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 1)
	original := page.reads[0]
	page.confirm = false
	if _, err := child.MessageStep(permit, snapshot, true); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil || page.sends != 1 || checkpoint.Replied != 5 || checkpoint.ReplyStats["unknown"] != 1 {
		t.Fatal("未知发送被计为成功或未保存", err)
	}
	page.reads = append(page.reads, original)
	if _, err = child.MessageStep(permit, snapshot, true); err != nil {
		t.Fatal(err)
	}
	if page.sends != 1 {
		t.Fatal("未知发送被再次执行")
	}
}

// TestPlanMessageStepRejectsInactive 验证未激活、不同占用、未来项、收尾与已失败都不会进入页面发送。
func TestPlanMessageStepRejectsInactive(t *testing.T) {
	for _, scenario := range []string{"inactive", "wrong_owner", "unbound", "future", "draining", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			child, permit, snapshot, page := planMessageFixture(t, 2)
			switch scenario {
			case "inactive":
				permit.Run.Items[0].Actions["auto_reply"] = planmodel.ActionProgress{State: "completed", Count: 5}
			case "wrong_owner":
				permit.Owner.OwnerID = "40000000-0000-0000-0000-000000000099"
				permit.Run.OwnerID = permit.Owner.OwnerID
			case "unbound":
				child.ownerID = ""
			case "future":
				permit.Run.Items[0].State = "pending"
			case "draining":
				permit.Run.State = "draining"
				permit.Owner.State = "releasing"
			case "failed":
				child.parent.runner.failStart(child.positionID, "fixture failure", child.state.options)
			}
			if _, err := child.MessageStep(permit, snapshot, true); err == nil {
				t.Fatal("不允许的动作被执行")
			}
			if page.sends != 0 || page.readCalls != 0 || len(page.timeline) != 0 {
				t.Fatal("拒绝后仍进入消息页面")
			}
			if !child.parent.Valid() {
				t.Fatal("拒绝步骤释放了父占用")
			}
		})
	}
}

// TestPlanPrimaryCompletedMessagesKeepAttribution 验证主项完成后的已激活消息仍归旧任务，不改记当前下一项。
func TestPlanPrimaryCompletedMessagesKeepAttribution(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 1)
	permit.Run.CurrentItem = 1
	permit.Run.Items[0].State = "completed"
	step, err := child.MessageStep(permit, snapshot, true)
	if err != nil || page.sends != 1 || step.TaskRunID != permit.Run.Items[0].TaskRunID || step.ItemRunID != permit.Run.Items[0].ID {
		t.Fatal("旧消息归属被改到新主项", step, err)
	}
}

// TestPlanMessageCleanupWaitsForBatch 验证发送批次仍执行时，子引用不能提前交还给下一岗位。
func TestPlanMessageCleanupWaitsForBatch(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 1)
	entered := make(chan struct{})
	resume := make(chan struct{})
	page.generate = func(context.Context) (string, error) {
		close(entered)
		<-resume
		return "受控回复", nil
	}
	finished := make(chan error, 1)
	go func() { _, err := child.MessageStep(permit, snapshot, true); finished <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("受控批次没有开始")
	}
	released := make(chan error, 1)
	go func() { released <- child.ReleaseAfterCleanup(true) }()
	select {
	case err := <-released:
		close(resume)
		t.Fatal("批次进行中交还了子引用", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-released; err != nil || !child.parent.Valid() {
		t.Fatal("批次结束后没有保留父占用", err)
	}
}

// TestPlanMessageSelectedActionPreservesOtherQueue 验证指定复打步骤不执行已有回复队列，下一回复步骤仍保留队列和原归属。
func TestPlanMessageSelectedActionPreservesOtherQueue(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 4, func(p *planmodel.Permit) {
		p.Run.Snapshot.Items[0].Actions = []string{"auto_reply", "re_greet"}
		p.Run.Items[0].Snapshot = p.Run.Snapshot.Items[0]
		p.Run.Items[0].Actions["re_greet"] = planmodel.ActionProgress{State: "active"}
	})
	for _, current := range page.reads {
		child.messages.replies = append(child.messages.replies, current.Conversation)
	}
	child.messages.scheduler.Checked(child.messages.now())
	step, err := child.MessageActionStep(permit, snapshot, false, "re_greet")
	if err != nil || !step.NoWork || step.Remaining || page.sends != 0 || len(child.messages.replies) != 4 {
		t.Fatal("指定动作清掉或执行了另一个动作队列", step, err)
	}
	step, err = child.MessageActionStep(permit, snapshot, false, "auto_reply")
	if err != nil || page.sends != 3 || !step.Remaining || step.TaskRunID != permit.Run.Items[0].TaskRunID {
		t.Fatal("原回复队列无法继续", step, err)
	}
}

// TestPlanMessageUsesLatestStartedPriority 验证采用同岗位新项策略时，原队列发送和累计值仍属于原任务。
func TestPlanMessageUsesLatestStartedPriority(t *testing.T) {
	child, permit, snapshot, page := planMessageFixture(t, 1)
	permit.Run.Snapshot.Items[1].Actions = []string{"auto_reply"}
	permit.Run.Items[1].Snapshot = permit.Run.Snapshot.Items[1]
	permit.Run.Items[1].Actions = map[string]planmodel.ActionProgress{"auto_reply": {State: "active"}}
	permit.Run.Items[1].State = "running"
	permit.Run.Items[1].TaskRunID = "70000000-0000-0000-0000-000000000002"
	permit.Run.Items[0].State = "completed"
	permit.Run.CurrentItem = 1
	cp, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), child.state.options.LocalRunID)
	if err != nil {
		t.Fatal(err)
	}
	latest := cp
	latest.ItemRunID = permit.Run.Items[1].ID
	latest.CloudRunID = permit.Run.Items[1].TaskRunID
	latest.PrioritizeReply = false
	latest.AccountBound = false
	if _, err = child.parent.runner.db.EnsurePlanActionRun(t.Context(), latest); err != nil {
		t.Fatal(err)
	}
	step, err := child.MessageActionStep(permit, snapshot, true, "auto_reply")
	if err != nil || page.sends != 1 || child.messages.scheduler.PrioritizeReply || step.TaskRunID != cp.CloudRunID {
		t.Fatal("新策略未生效或改写原任务归属", step, err)
	}
	old, err := child.parent.runner.db.LoadActionCheckpoint(t.Context(), cp.RunID)
	if err != nil || old.Replied != 6 || !old.PrioritizeReply {
		t.Fatal("发送被改计到新项或改写原项配置", err)
	}
}
