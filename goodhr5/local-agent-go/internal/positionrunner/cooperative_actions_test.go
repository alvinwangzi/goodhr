// 本文件用真实 SQLite 与可控页面边界验证 HRPlus 实际单岗位调度、消息检查、公平性和停止。
package positionrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// cooperativeReGreetFixture 复用实际复打流程的页面边界，让同名候选人分别按 ID 定位。
type cooperativeReGreetFixture struct {
	*cooperativeFixture
	reGreet *reGreetFixture
}

// LocateReplyConversation 保留旧接口以满足平台能力，实际新调度不允许用姓名定位发送。
func (f *cooperativeReGreetFixture) LocateReplyConversation(context.Context, platformcore.Executor, string) (platformcore.ReplyConversation, error) {
	return platformcore.ReplyConversation{}, platformcore.ErrReplyUnsafe
}

// LocateReplyConversationByID 设置已核对的会话 ID，姓名不能选择目标。
func (f *cooperativeReGreetFixture) LocateReplyConversationByID(_ context.Context, _ platformcore.Executor, name, id string) (platformcore.ReplyConversation, error) {
	f.reGreet.current.Conversation = platformcore.ReplyConversation{ID: id, Name: name, PositionName: "Go"}
	return f.reGreet.current.Conversation, nil
}

// ReadOpenedReplyContext 返回对应 ID 的页面上下文。
func (f *cooperativeReGreetFixture) ReadOpenedReplyContext(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	return f.reGreet.ReadOpenedReplyContext(ctx, exec, target, conversation)
}

// StageReGreet 复用受控输入边界。
func (f *cooperativeReGreetFixture) StageReGreet(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, before platformcore.ReplyContext, text string) error {
	return f.reGreet.StageReGreet(ctx, exec, target, conversation, before, text)
}

// SendReGreet 记录实际跨越发送边界的次数。
func (f *cooperativeReGreetFixture) SendReGreet(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, before platformcore.ReplyContext, text string) error {
	return f.reGreet.SendReGreet(ctx, exec, target, conversation, before, text)
}

// ConfirmReGreet 复用发送确认边界，收据和 SQLite 使用实际实现。
func (f *cooperativeReGreetFixture) ConfirmReGreet(ctx context.Context, exec platformcore.Executor, target platformcore.ReplyTarget, conversation platformcore.ReplyConversation, before platformcore.ReplyContext, text string) (bool, error) {
	return f.reGreet.ConfirmReGreet(ctx, exec, target, conversation, before, text)
}

// GenerateReGreet 生成受控测试文本，不请求真实 AI 或发送给真实候选人。
func (f *cooperativeReGreetFixture) GenerateReGreet(ctx context.Context, request localai.ReGreetRequest) (localai.ReGreetDecision, error) {
	return f.reGreet.GenerateReGreet(ctx, request)
}

// TestCooperativeReGreetBatchRetainsUnclaimed 验证首人耗时过长时剩余名单不丢失，第二批后让出扫描且不阻塞几十分钟。
func TestCooperativeReGreetBatchRetainsUnclaimed(t *testing.T) {
	s, base, now := cooperativeSessionFixture(t, false, 0)
	s.options.TaskType = "greeting,re_greet"
	s.options.ReGreetIntervalMin = 30
	s.options.ReGreetIntervalMax = 30
	f := &cooperativeReGreetFixture{cooperativeFixture: base, reGreet: &reGreetFixture{replyFixture: base.replyFixture, confirmed: true, current: platformcore.ReplyContext{ResumeStatus: "none", Messages: []platformcore.ReplyMessage{{Direction: "outbound", Kind: "text", Text: "首次招呼"}}}}}
	f.reGreet.onGenerate = func() { *now = now.Add(61 * time.Second); f.reGreet.onGenerate = nil }
	s.runtime = f
	s.flow.runtime = f
	s.flow.generator = f
	basis := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339Nano)
	candidates := []map[string]any{}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("geek-%d", i)
		candidates = append(candidates, map[string]any{"platform_candidate_id": id, "candidate_name": "同名", "greeted_at": basis, "re_greet_count": 0})
		if err := s.runner.db.SaveCandidateIdentity(t.Context(), localdb.CandidateIdentity{ProfileScope: s.flow.scope, Platform: "boss", RecommendationID: id, ConversationID: "conversation-" + id, Status: "verified", Source: "controlled-direct-transition"}); err != nil {
			t.Fatal(err)
		}
	}
	var reports atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/re-greet-candidates") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "re_greet_receipts": true, "items": candidates})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/re-greet-report") {
			var request cloudapi.ReGreetReceiptRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Success {
				reports.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: request.OperationID, ResultCount: request.BaseCount + 1, SentAt: request.SentAt, ReceivedAt: time.Now()}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()
	s.options.CloudAPIBase = server.URL
	s.options.Token = "fixture-token"
	s.flow.cloudClient = cloudapi.New(server.URL)
	started := time.Now()
	if _, err := s.service(t.Context(), true, true); err != nil {
		t.Fatal(err)
	}
	if f.reGreet.sent != 4 || len(s.reGreets) != 4 || s.reGreetStats.total != 8 || reports.Load() != 4 || time.Since(started) > 10*time.Second {
		t.Fatalf("复打批次丢失或阻塞：sent=%d pending=%d total=%d reports=%d", f.reGreet.sent, len(s.reGreets), s.reGreetStats.total, reports.Load())
	}
}

// cooperativeFixture 替换平台页面事实，不替换实际调度、回复编排或发送存储。
type cooperativeFixture struct {
	platformcore.Runtime
	*replyFixture
	matched                    bool
	captures, returns, rewinds int
	timeline                   []string
}

// PrepareReplyPage 记录真实调度选择的消息入口。
func (f *cooperativeFixture) PrepareReplyPage(context.Context, platformcore.Executor) error {
	f.timeline = append(f.timeline, "messages")
	return nil
}

// ScanUnreadReplies 返回尚未读取的会话，模拟点击后成为已读。
func (f *cooperativeFixture) ScanUnreadReplies(context.Context, platformcore.Executor, platformcore.ReplyTarget, int) ([]platformcore.ReplyConversation, error) {
	conversations := []platformcore.ReplyConversation{}
	for _, current := range f.reads {
		conversations = append(conversations, current.Conversation)
	}
	return conversations, nil
}

// CaptureRecommendationCursor 记录切换前锚点，数据来自实际 SQLite 检查点。
func (f *cooperativeFixture) CaptureRecommendationCursor(_ context.Context, _ platformcore.Executor, anchors []string) (platformcore.RecommendationCursor, error) {
	f.captures++
	return platformcore.RecommendationCursor{Valid: len(anchors) > 0, Anchors: anchors, Signature: "verified-filter"}, nil
}

// ReturnToRecommendation 记录当前批次完成后再返回推荐。
func (f *cooperativeFixture) ReturnToRecommendation(context.Context, platformcore.Executor) error {
	f.returns++
	f.timeline = append(f.timeline, "recommendation")
	return nil
}

// CheckRecommendationCursor 允许测试局部锚点不匹配的真实回退分支。
func (f *cooperativeFixture) CheckRecommendationCursor(_ context.Context, _ platformcore.Executor, cursor platformcore.RecommendationCursor) (bool, string, error) {
	return f.matched && cursor.Valid, "fixture_anchor", nil
}

// RewindRecommendation 记录回退，不伪造候选人完成结果。
func (f *cooperativeFixture) RewindRecommendation(context.Context, platformcore.Executor) error {
	f.rewinds++
	return nil
}

// CurrentPositionName 返回页面当前岗位，发送身份仍由实际回复流程核对。
func (f *cooperativeFixture) CurrentPositionName(context.Context, platformcore.Executor, cloudapi.PlatformConfig) (string, error) {
	return "Go", nil
}

// cooperativeSessionFixture 建立同一运行的实际检查点和回复依赖，时钟可由测试推进。
func cooperativeSessionFixture(t *testing.T, priority bool, count int) (*actionSession, *cooperativeFixture, *time.Time) {
	t.Helper()
	flow, base, current := newReplyFlowFixture(t)
	r := newTestRunner(t, flow.db, &onceWorker{})
	position, err := r.db.CreatePosition(map[string]any{"name": "Go", "platform_id": "boss"})
	if err != nil {
		t.Fatal(err)
	}
	flow.positionID = position.ID
	flow.scope = platformcore.ReplyHash("profile:default")
	flow.exec = &resumeDelayExecutor{}
	checkpoint, err := r.db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: position.ID, ProfileScope: flow.scope, Platform: "boss", Greeted: 7, Anchors: []string{"A", "B", "C"}, TaskType: "greeting,auto_reply", PrioritizeReply: priority})
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{LocalRunID: checkpoint.RunID, CloudRunID: flow.runID, TaskType: "greeting,auto_reply", PrioritizeReply: priority, EnableGreet: true}
	ctx, cancel := context.WithCancel(t.Context())
	if !r.setRunning(position.ID, cancel, options) {
		t.Fatal("运行锁未取得")
	}
	t.Cleanup(func() { cancel(); r.clear(position.ID) })
	f := &cooperativeFixture{replyFixture: base, matched: true}
	flow.runtime = f
	for i := 0; i < count; i++ {
		copy := current
		copy.Conversation.ID = fmt.Sprintf("conversation-%d", i)
		base.reads = append(base.reads, copy)
	}
	now := time.Now()
	s := &actionSession{runner: r, position: position, options: options, runtime: f, resumer: f, flow: flow, scheduler: actiondispatch.New(priority), now: func() time.Time { return now }, handledReGreets: map[string]bool{}, onRecommendation: true}
	_ = ctx
	return s, f, &now
}

// TestCooperativePriorityAndFairness 验证实际发送六人后让出扫描，同一运行的数量和剩余队列不重置。
func TestCooperativePriorityAndFairness(t *testing.T) {
	s, f, _ := cooperativeSessionFixture(t, true, 8)
	rescan, err := s.service(t.Context(), true, true)
	if err != nil || rescan || f.sends != 6 || len(s.replies) != 2 || f.captures != 1 || f.rewinds != 0 {
		t.Fatalf("调度未按安全批次让出 %+v err=%v sends=%d", s.replyStats, err, f.sends)
	}
	checkpoint, err := s.runner.db.LoadActionCheckpoint(t.Context(), s.options.LocalRunID)
	if err != nil || checkpoint.Greeted != 7 || checkpoint.Replied != 6 {
		t.Fatalf("切换重置了计数 %+v %v", checkpoint, err)
	}
	if _, err = s.scanBoundary(t.Context()); err != nil || f.sends != 8 {
		t.Fatalf("优先回复未继续已发现消息 %v sends=%d", err, f.sends)
	}
}

// TestCooperativeTimedReplyWithoutPriority 验证关闭优先回复时持续扫描，到 60 秒安全检查才继续消息。
func TestCooperativeTimedReplyWithoutPriority(t *testing.T) {
	s, f, now := cooperativeSessionFixture(t, false, 8)
	if _, err := s.service(t.Context(), true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanBoundary(t.Context()); err != nil || f.sends != 6 {
		t.Fatalf("关闭优先回复仍连续抢占 %v %d", err, f.sends)
	}
	*now = now.Add(59 * time.Second)
	if _, err := s.scanBoundary(t.Context()); err != nil || f.sends != 6 {
		t.Fatalf("检查提前触发 %v %d", err, f.sends)
	}
	*now = now.Add(time.Second)
	if _, err := s.scanBoundary(t.Context()); err != nil || f.sends != 8 {
		t.Fatalf("到期后未检查回复 %v %d", err, f.sends)
	}
}

// TestCooperativeRescanAndStop 验证锚点变化返回真实恢复指令，停止后不再输入或领取消息。
func TestCooperativeRescanAndStop(t *testing.T) {
	s, f, _ := cooperativeSessionFixture(t, true, 1)
	f.matched = false
	rescan, err := s.service(t.Context(), true, true)
	if err != nil || !rescan || f.rewinds != 1 {
		t.Fatalf("未走回退 %v %v", rescan, err)
	}
	before := len(f.timeline)
	s.runner.markUserStopped(s.position.ID)
	if _, err = s.scanBoundary(t.Context()); !errors.Is(err, context.Canceled) || len(f.timeline) != before {
		t.Fatal("停止后仍操作页面")
	}
}

// TestCooperativeNoCurrentWorkEnds 验证无当前消息即返回，不等待未来周期检查。
func TestCooperativeNoCurrentWorkEnds(t *testing.T) {
	s, f, _ := cooperativeSessionFixture(t, true, 0)
	s.onRecommendation = false
	started := time.Now()
	if _, err := s.service(t.Context(), false, true); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second || f.returns != 0 || f.sends != 0 {
		t.Fatal("无工作仍挂着")
	}
}

// TestCooperativeNewMessageDuringScan 验证最初无消息，扫描期间到期后出现消息立即处理。
func TestCooperativeNewMessageDuringScan(t *testing.T) {
	s, f, now := cooperativeSessionFixture(t, true, 0)
	if _, err := s.service(t.Context(), true, true); err != nil {
		t.Fatal(err)
	}
	current := newReplyConversation()
	f.reads = append(f.reads, current)
	*now = now.Add(time.Minute)
	if _, err := s.scanBoundary(t.Context()); err != nil || f.sends != 1 {
		t.Fatalf("扫描中消息未被处理 %v %d", err, f.sends)
	}
}

// dummyCloudForCooperative 供调度异常测试复用独立 HTTP 服务，不发送真实消息或邮件。
func dummyCloudForCooperative(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server
}
