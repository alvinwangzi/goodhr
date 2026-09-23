// 本文件验证自动回复编排、停止和跨任务浏览器独占，不访问招聘网站。
package positionrunner

import (
 "context"
 "errors"
 "testing"

 "goodhr5/local-agent-go/internal/browser"
 "goodhr5/local-agent-go/internal/localai"
 "goodhr5/local-agent-go/internal/localdb"
 "goodhr5/local-agent-go/internal/platformcore"
)

// replyFixture 将真实页面边界替换为可控事实，主流程和 SQLite 仍使用真实实现。
type replyFixture struct {
 platformcore.AutoReplyRuntime
 current platformcore.ReplyContext
 scan    []platformcore.ReplyConversation
 reads   []platformcore.ReplyContext
 readCalls, prepared int
 send func() (bool, error)
 stage func() error
 confirm bool
 stale bool
 sends, generations int
 generate func(context.Context) (string,error)
}

// AutoReplyAvailable 表示测试平台配置已通过验证。
func (f *replyFixture) AutoReplyAvailable() error { return nil }

// PrepareReplyPage 记录消息页准备次数。
func (f *replyFixture) PrepareReplyPage(context.Context, platformcore.Executor) error { f.prepared++; return nil }

// ResolveReplyTarget 返回与岗位匹配的固定目标。
func (f *replyFixture) ResolveReplyTarget(context.Context, platformcore.Executor, string) (platformcore.ReplyTarget, error) {
 return platformcore.ReplyTarget{PositionID: "job1", PositionName: "Go", NameUnique: true}, nil
}

// ScanUnreadReplies 返回预设的未读会话列表。
func (f *replyFixture) ScanUnreadReplies(context.Context, platformcore.Executor, platformcore.ReplyTarget, int) ([]platformcore.ReplyConversation, error) {
 return f.scan, nil
}

// ReadReplyContext 按顺序返回预设会话内容，并记录读取次数。
func (f *replyFixture) ReadReplyContext(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
 f.readCalls++
 if len(f.reads) == 0 { return platformcore.ReplyContext{}, platformcore.ErrReplyUnsafe }
 value := f.reads[0]
 f.reads = f.reads[1:]
 return value, nil
}

// GenerateReply 模拟远程 AI 请求，可在请求中触发停止或返回错误。
func (f *replyFixture) GenerateReply(ctx context.Context, _ localai.ReplyRequest) (string,error) {
 f.generations++
 if f.generate != nil { return f.generate(ctx) }
 return "你好",nil
}
// RecheckReplyContext 模拟生成期间新消息或人工回复使旧答案失效。
func (f *replyFixture) RecheckReplyContext(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, c platformcore.ReplyContext) (platformcore.ReplyContext,error) {
 if f.stale { return c,platformcore.ErrReplyUnsafe }; return c,nil
}
// StageReply 模拟输入完成，允许测试在点击发送前取消。
func (f *replyFixture) StageReply(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyContext,string) error {
 if f.stage != nil { return f.stage() }; return nil
}
// SendReply 模拟真正的外部发送边界，记录发送次数。
func (f *replyFixture) SendReply(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyContext,string) (bool,error) {
 f.sends++; if f.send != nil { return f.send() }; return true,nil
}
// ConfirmReply 模拟页面是否已有对应的我方消息。
func (f *replyFixture) ConfirmReply(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation,string,string) (bool,error) { return f.confirm,nil }
// HasPendingResumeOffer 模拟检测候选人是否已主动发简历，默认返回 false。
func (f *replyFixture) HasPendingResumeOffer(context.Context, platformcore.Executor) (bool,error) { return false,nil }
// AcceptPendingResumeOffer 模拟点击同意接受简历。
func (f *replyFixture) AcceptPendingResumeOffer(context.Context, platformcore.Executor) error { return nil }

// newReplyFlowFixture 为防重测试创建真实存储、稳定会话和模拟的外部能力。
func newReplyFlowFixture(t *testing.T) (*replyFlow,*replyFixture,platformcore.ReplyContext) {
 t.Helper()
 f := &replyFixture{confirm:true}
 c := platformcore.ReplyContext{Conversation:platformcore.ReplyConversation{ID:"c1",PositionID:"job1"}, Messages:[]platformcore.ReplyMessage{{ID:"m1",Direction:"inbound",Kind:"text",Text:"你好"}}}
 flow := &replyFlow{db:openRunnerTestDB(t), runtime:f, generator:f, target:platformcore.ReplyTarget{PositionID:"job1"}, scope:"profile-hash", platform:"boss", positionID:"position1", runID:"run1"}
 return flow,f,c
}

// TestReplyFlowDedup 验证发送先落库，并且跨任务换措辞不会重复调用 AI 或发送。
func TestReplyFlowDedup(t *testing.T) {
 flow,f,c := newReplyFlowFixture(t)
 f.send = func() (bool,error) {
  record,err := flow.db.FindAutoReply(t.Context(),localdb.AutoReplyRecord{ProfileScope:"profile-hash",Platform:"boss",ConversationID:"c1",InboundFingerprint:platformcore.ReplyMessageFingerprint("c1",c.Messages[0])})
  if err != nil || record.Status != "sending" { t.Fatalf("发送前未落库：%+v %v",record,err) }
  return true,nil
 }
 outcome,err := flow.process(t.Context(),c)
 if err != nil || outcome != "sent" { t.Fatalf("处理失败：%s %v",outcome,err) }
 flow.runID = "run2"; flow.positionID = "position2"
 outcome,err = flow.process(t.Context(),c)
 if outcome != "skipped" || f.sends != 1 || f.generations != 1 { t.Fatalf("重复回复：%s %v %+v",outcome,err,f) }
}

// TestReplyFlowSafetyFailures 验证停止、过期、草稿、AI错误和存储失败时发送次数为零。
func TestReplyFlowSafetyFailures(t *testing.T) {
 for _, name := range []string{"停止生成","停止输入后","过期","草稿","AI错误","存储失败","我方消息","系统消息"} {
  t.Run(name,func(t *testing.T){
   flow,f,c := newReplyFlowFixture(t)
   ctx,cancel := context.WithCancel(t.Context()); defer cancel()
   switch name {
   case "停止生成": f.generate=func(context.Context)(string,error){cancel();return "旧答案",nil}
   case "停止输入后": f.stage=func()error{cancel();return nil}
   case "过期": f.stale=true
   case "草稿": c.Draft="人工文字"
   case "AI错误": f.generate=func(context.Context)(string,error){return "",errors.New("外部请求失败")}
   case "存储失败": _=flow.db.Close()
   case "我方消息": c.Messages[0].Direction="outbound"
   case "系统消息": c.Messages[0].Kind="system"
   }
   _,_ = flow.process(ctx,c)
   if f.sends != 0 { t.Fatalf("不安全条件下发送了 %d 次",f.sends) }
  })
 }
}

// TestReplyFlowUnknown 验证点击超时仅核对页面，不自动再次发送；已发送后停止仍完成必要确认。
func TestReplyFlowUnknown(t *testing.T) {
 flow,f,c := newReplyFlowFixture(t)
 f.confirm=false
 f.send=func()(bool,error){return true,context.DeadlineExceeded}
 outcome,err := flow.process(t.Context(),c)
 if err != nil || outcome != "unknown" { t.Fatalf("结果不明未保留：%s %v",outcome,err) }
 _,_ = flow.process(t.Context(),c)
 if f.sends != 1 || f.generations != 1 { t.Fatal("结果不明被重发") }
 f.confirm=true
 outcome,err=flow.process(t.Context(),c)
 if outcome != "skipped" || f.sends != 1 { t.Fatalf("未知结果应跳过：%s %v",outcome,err) }
 flow,f,c = newReplyFlowFixture(t)
 ctx,cancel:=context.WithCancel(t.Context()); defer cancel()
 f.send=func()(bool,error){cancel();return true,nil}
 outcome,err=flow.process(ctx,c)
 if err != nil || outcome != "sent" { t.Fatalf("发送开始后未确认结果：%s %v",outcome,err) }
}

// TestReplyTaskConfiguration 验证任务默认、配置提示词隔离和自动回复独立轮次。
func TestReplyTaskConfiguration(t *testing.T) {
 if got,err:=normalizeTaskType(""); err!=nil || got!="greeting" { t.Fatal(got,err) }
 if _,err:=normalizeTaskType("other"); err==nil { t.Fatal("接受了未知任务") }
 if scanRounds(StartOptions{TaskType:"auto_reply"})!=1 { t.Fatal("回复错误沿用了三轮默认") }
 cfg:=aiConfigFromCloud(map[string]any{"reply_system_prompt":"回复规则","prompt_template":"评分规则"})
 if cfg.ReplySystemPrompt!="回复规则" { t.Fatal("回复配置未透传") }
}

// TestBrowserLeaseAcrossTasks 验证不同岗位不能并发，取消后必须等旧任务退出再释放。
func TestBrowserLeaseAcrossTasks(t *testing.T) {
 r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
 ctx, cancel := context.WithCancel(context.Background())
 if !r.setRunning("one", cancel, StartOptions{}) { t.Fatal("首任务未取得占用") }
 if r.setRunning("two", func(){}, StartOptions{}) { t.Fatal("不同岗位抢占了浏览器") }
 r.markUserStoppedAndCancel("one")
 if ctx.Err() == nil { t.Fatal("停止未取消上下文") }
 if r.setRunning("two", func(){}, StartOptions{}) { t.Fatal("旧任务退出前释放了占用") }
 r.clear("one")
 if !r.setRunning("two", func(){}, StartOptions{}) { t.Fatal("退出后未释放占用") }
 r.clear("two")
}

// TestResumeBrowserLeaseHandoff 验证收尾预先继承占用，并等待主任务退出后才操作页面。
func TestResumeBrowserLeaseHandoff(t *testing.T) {
 r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
 r.setRunning("one", func(){}, StartOptions{})
 release, ready, ok := r.reserveResumeBrowser("one")
 if !ok { t.Fatal("无法继承占用") }
 select { case <-ready: t.Fatal("主任务未退出就允许收尾"); default: }
 r.clear("one")
 select { case <-ready: default: t.Fatal("主任务退出后未唤醒收尾") }
 if r.setRunning("two", func(){}, StartOptions{}) { t.Fatal("收尾未结束却开始新任务") }
 if _, _, ok := r.reserveResumeBrowser("two"); ok { t.Fatal("繁忙时允许另一个收尾") }
 release()
 if !r.setRunning("two", func(){}, StartOptions{}) { t.Fatal("收尾后未释放") }
 r.clear("two")
}

// onceWorker 证明自动回复浏览器动作只走不重试调用，旧 Call 路径被拒绝。
type onceWorker struct{ calls []string }

// Start 返回浏览器已启动。
func (w *onceWorker) Start(context.Context) (browser.WorkerStatus, error) { return browser.WorkerStatus{Running: true}, nil }

// Call 模拟旧重试路径并拒绝服务。
func (w *onceWorker) Call(context.Context, string, any) (map[string]any, error) {
 return nil, errors.New("自动回复不应使用可重试调用")
}

// CallOnce 记录调用路径并返回成功。
func (w *onceWorker) CallOnce(_ context.Context, path string, _ any) (map[string]any, error) {
 w.calls = append(w.calls, path)
 return map[string]any{"data": map[string]any{}}, nil
}

// newReplyConversation 返回属于当前岗位的一条候选人文本会话。
func newReplyConversation() platformcore.ReplyContext {
 return platformcore.ReplyContext{Conversation: platformcore.ReplyConversation{ID: "c1", PositionID: "job1", PositionName: "Go"}, Messages: []platformcore.ReplyMessage{{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"}}}
}

// TestRunAutoReplyRound 验证整轮编排：只走不重试调用、逐会话处理并聚合统计。
func TestRunAutoReplyRound(t *testing.T) {
 worker := &onceWorker{}
 r := newTestRunner(t, openRunnerTestDB(t), worker)
 fixture := &replyFixture{confirm: true}
 c := newReplyConversation()
 fixture.scan = []platformcore.ReplyConversation{c.Conversation}
 fixture.reads = []platformcore.ReplyContext{c}
 options := StartOptions{TaskType: "auto_reply", ScanRounds: 1, CloudRunID: "run-1"}
 stats := r.runAutoReply(t.Context(), localdb.Position{ID: "p1", Name: "Go", PlatformID: "boss"}, options, fixture, fixture, nil)
 if stats.Replied != 1 || stats.Checked != 1 || fixture.sends != 1 || fixture.prepared != 1 {
  t.Fatalf("整轮编排错误：%+v sends=%d prepared=%d", stats, fixture.sends, fixture.prepared)
 }
 if len(worker.calls) == 0 || worker.calls[0] != "/api/v1/browser/start" {
  t.Fatalf("浏览器启动顺序错误：%v", worker.calls)
 }
}

// TestRunAutoReplyFailureLimit 验证连续会话错误三次后整任务停止，不再读取后续会话。
func TestRunAutoReplyFailureLimit(t *testing.T) {
 r := newTestRunner(t, openRunnerTestDB(t), &onceWorker{})
 fixture := &replyFixture{confirm: true}
 fixture.send = func() (bool, error) { return false, errors.New("页面动作失败") }
 c := newReplyConversation()
 fixture.scan = []platformcore.ReplyConversation{c.Conversation, c.Conversation, c.Conversation, c.Conversation}
 fixture.reads = []platformcore.ReplyContext{c, c, c, c}
 stats := r.runAutoReply(t.Context(), localdb.Position{ID: "p1", Name: "Go", PlatformID: "boss"}, StartOptions{TaskType: "auto_reply", ScanRounds: 1, CloudRunID: "run-1"}, fixture, fixture, nil)
 if stats.Failed != 3 || fixture.readCalls != 3 {
  t.Fatalf("连续失败未按三次停止：%+v reads=%d", stats, fixture.readCalls)
 }
}

// TestAutoReplyStopCancelsImmediately 验证自动回复任务可被识别并在停止时立即取消。
func TestAutoReplyStopCancelsImmediately(t *testing.T) {
 r := newTestRunner(t, openRunnerTestDB(t), &fakeWorker{})
 _, cancel := context.WithCancel(context.Background())
 defer cancel()
 if !r.setRunning("p1", cancel, StartOptions{TaskType: "auto_reply"}) { t.Fatal("未取得占用") }
 if !r.autoReplyRunning("p1") { t.Fatal("未识别自动回复任务") }
 if r.autoReplyRunning("missing") { t.Fatal("未知岗位误报") }
 r.clear("p1")
 if r.autoReplyRunning("p1") { t.Fatal("清理后仍误报") }
}
