// 本文件覆盖复打招呼相关的本地 Agent 逻辑：任务类型识别、个人配置抽取、随机间隔。
package positionrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"goodhr5/local-agent-go/internal/localai"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// reGreetFixture 只替换浏览器和 AI 边界，复用真实岗位流程和 SQLite 发送记录。
type reGreetFixture struct {
	*replyFixture
	platformcore.ReGreetRuntime
	current         platformcore.ReplyContext
	request         localai.ReGreetRequest
	generated, sent int
	confirmed       bool
	onGenerate      func()
	onStage         func()
}

// LocateReplyConversation 返回当前模拟候选人身份。
func (f *reGreetFixture) LocateReplyConversation(context.Context, platformcore.Executor, string) (platformcore.ReplyConversation, error) {
	return f.current.Conversation, nil
}

// LocateReplyConversationByID 模拟真实 ID 核对，错误目标不返回可发送会话。
func (f *reGreetFixture) LocateReplyConversationByID(_ context.Context, _ platformcore.Executor, _ string, id string) (platformcore.ReplyConversation, error) {
	if id != f.current.Conversation.ID {
		return platformcore.ReplyConversation{}, platformcore.ErrReplyUnsafe
	}
	return f.current.Conversation, nil
}

// ReadOpenedReplyContext 返回当前页面的副本，以便发现生成期间的新消息。
func (f *reGreetFixture) ReadOpenedReplyContext(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation) (platformcore.ReplyContext, error) {
	c := f.current
	c.Messages = append([]platformcore.ReplyMessage{}, c.Messages...)
	return c, nil
}

// GenerateReGreet 记录真实传入 AI 的配置，并允许模拟生成期间的页面变动。
func (f *reGreetFixture) GenerateReGreet(_ context.Context, request localai.ReGreetRequest) (localai.ReGreetDecision, error) {
	f.generated++
	f.request = request
	if f.onGenerate != nil {
		f.onGenerate()
	}
	return localai.ReGreetDecision{ShouldSend: true, Message: "再次沟通岗位"}, nil
}

// StageReGreet 模拟输入完成，不修改真实页面。
func (f *reGreetFixture) StageReGreet(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, platformcore.ReplyContext, string) error {
	if f.onStage != nil {
		f.onStage()
	}
	return nil
}

// SendReGreet 记录跨越发送边界的次数。
func (f *reGreetFixture) SendReGreet(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, platformcore.ReplyContext, string) error {
	f.sent++
	return nil
}

// ConfirmReGreet 返回预设页面确认结果。
func (f *reGreetFixture) ConfirmReGreet(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, platformcore.ReplyContext, string) (bool, error) {
	return f.confirmed, nil
}

// TestRunReGreetSafety 验证完整主流程的跳过、配置透传、发送防重及停止保护。
func TestRunReGreetSafety(t *testing.T) {
	for _, scenario := range []string{"sent", "replied", "received", "changed", "stopped", "unknown", "report_failed"} {
		t.Run(scenario, func(t *testing.T) {
			f := &reGreetFixture{replyFixture: &replyFixture{}, confirmed: scenario != "unknown", current: platformcore.ReplyContext{Conversation: platformcore.ReplyConversation{ID: "c1", Name: "候选人", PositionName: "Go"}, ResumeStatus: "none", Messages: []platformcore.ReplyMessage{{Direction: "outbound", Kind: "text", Text: "首次招呼"}}}}
			if scenario == "replied" {
				f.current.Messages[0].Direction = "inbound"
			}
			if scenario == "received" {
				f.current.ResumeStatus = "received"
			}
			if scenario == "changed" {
				f.onGenerate = func() {
					f.current.Messages = append(f.current.Messages, platformcore.ReplyMessage{Direction: "inbound", Kind: "text", Text: "刚刚回复"})
				}
			}
			var reports []map[string]any
			var observations []map[string]any
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/re-greet-candidates") {
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "re_greet_receipts": true, "items": []map[string]any{{"platform_candidate_id": "candidate1", "candidate_name": "候选人", "greeted_at": time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Hour).Format(time.RFC3339Nano)}}})
					return
				}
				var payload map[string]any
				_ = json.NewDecoder(req.Body).Decode(&payload)
				if strings.HasSuffix(req.URL.Path, "/screenings") {
					observations = append(observations, payload)
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
					return
				}
				reports = append(reports, payload)
				if scenario == "report_failed" {
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))
			defer cloud.Close()
			r := newTestRunner(t, openRunnerTestDB(t), &onceWorker{})
			position, err := r.db.CreatePosition(map[string]any{"name": "Go", "platform_id": "boss"})
			if err != nil {
				t.Fatal(err)
			}
			position.PositionSnapshot = map[string]any{"ai_config": map[string]any{"re_greet_prompt": "岗位复打规则", "re_greet_skip_refused": true}}
			if err := r.db.SaveCandidateIdentity(t.Context(), localdb.CandidateIdentity{ProfileScope: platformcore.ReplyHash("profile:" + positionProfileName(position)), Platform: "boss", RecommendationID: "candidate1", ConversationID: "c1", Source: "controlled-direct-transition", Status: "verified"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := StartOptions{CloudAPIBase: cloud.URL, Token: "test", CloudRunID: "run1", TaskType: "re_greet"}
			r.setRunning(position.ID, cancel, options)
			defer r.clear(position.ID)
			if scenario == "stopped" {
				f.onStage = func() { r.markUserStoppedAndCancel(position.ID) }
			}
			r.runReGreet(ctx, position, options, PositionRuntimeSnapshot{}, f, f, f)
			wantSend := scenario == "sent" || scenario == "unknown" || scenario == "report_failed"
			if (f.sent == 1) != wantSend {
				t.Fatalf("发送次数错误：%d", f.sent)
			}
			if scenario == "replied" || scenario == "received" {
				if f.generated != 0 {
					t.Fatal("应跳过 AI")
				}
				if len(observations) != 1 {
					t.Fatalf("跳过前未同步页面状态：%v", observations)
				}
				item := observations[0]["items"].([]any)[0].(map[string]any)
				if item["platform_candidate_id"] != "candidate1" || item["source"] != "platform_observation" || item["contact_observed"] != true {
					t.Fatalf("页面事实归属错误：%v", item)
				}
				if scenario == "received" && item["resume_status"] != "received" {
					t.Fatalf("已收简历未回写：%v", item)
				}
			}
			if f.generated > 0 && (f.request.ReGreetPrompt != "岗位复打规则" || f.request.SkipRefusedCheck || f.request.GreetMessage != "首次招呼") {
				t.Fatalf("配置未接通：%+v", f.request)
			}
			if wantSend {
				// 云端继续返回同一名单，已发出或结果未知也不能再次发送。
				r.runReGreet(ctx, position, options, PositionRuntimeSnapshot{}, f, f, f)
				if f.sent != 1 || f.generated != 1 {
					t.Fatalf("重复发送：sent=%d AI=%d", f.sent, f.generated)
				}
			}
			if scenario == "sent" && (len(reports) != 1 || reports[0]["message_text"] != "再次沟通岗位" || reports[0]["success"] != true) {
				t.Fatalf("结果上报缺失：%v", reports)
			}
		})
	}
}

// TestReGreetMessageTaskOrder 验证组合任务无论勾选顺序均先复打再自动回复，失败不进入下一阶段。
func TestReGreetMessageTaskOrder(t *testing.T) {
	for _, value := range []string{"greeting,re_greet,auto_reply", "auto_reply,re_greet", "re_greet,auto_reply,greeting"} {
		var calls []string
		ok := executeMessageTasks(parseTaskTypes(value), func(task string) bool { calls = append(calls, task); return true })
		if !ok || !reflect.DeepEqual(calls, []string{"re_greet", "auto_reply"}) {
			t.Fatalf("任务顺序错误：%s %v", value, calls)
		}
	}
	var calls []string
	if executeMessageTasks(parseTaskTypes("re_greet,auto_reply"), func(task string) bool { calls = append(calls, task); return false }) || len(calls) != 1 {
		t.Fatalf("失败后仍进入下一阶段：%v", calls)
	}
}

// TestReGreetRules 验证真实岗位配置读取、已回复过滤与生成期间的上下文变动。
func TestReGreetRules(t *testing.T) {
	position := localdb.Position{PositionSnapshot: map[string]any{"ai_config": map[string]any{"re_greet_prompt": "岗位复打规则", "re_greet_skip_refused": true}}}
	if got := positionReGreetPrompt(position, "fallback"); got != "岗位复打规则" {
		t.Fatalf("没有读取岗位 ai_config 中的复打提示词：%q", got)
	}
	if !positionReGreetSkipRefused(position) {
		t.Fatal("没有读取拒绝开关")
	}
	base := platformcore.ReplyContext{ResumeStatus: "none", Messages: []platformcore.ReplyMessage{{Direction: "outbound", Kind: "text", Text: "首次招呼"}}}
	if reason := reGreetSkipReason(base); reason != "" {
		t.Fatalf("未回复会话被跳过：%s", reason)
	}
	if firstReGreetMessage(base.Messages) != "首次招呼" {
		t.Fatal("首次招呼内容缺失")
	}
	for _, tc := range []struct {
		name, reason string
		current      platformcore.ReplyContext
	}{
		{"已回复", "skipped_replied", platformcore.ReplyContext{Messages: []platformcore.ReplyMessage{{Direction: "inbound", Kind: "text", Text: "你好"}}}},
		{"已收到简历", "skipped_resume_received", platformcore.ReplyContext{ResumeStatus: "received", Messages: base.Messages}},
		{"空上下文", "context_unsafe", platformcore.ReplyContext{}},
		{"未知方向", "context_unsafe", platformcore.ReplyContext{Messages: []platformcore.ReplyMessage{{Direction: "", Kind: "text"}}}},
		{"人工草稿", "context_unsafe", platformcore.ReplyContext{Draft: "人工输入", Messages: base.Messages}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reGreetSkipReason(tc.current); got != tc.reason {
				t.Fatalf("got=%q want=%q", got, tc.reason)
			}
		})
	}
	changed := base
	changed.Messages = append(append([]platformcore.ReplyMessage{}, base.Messages...), platformcore.ReplyMessage{Direction: "inbound", Kind: "text", Text: "刚刚回复"})
	if reGreetContextMatches(base, changed) {
		t.Fatal("生成期间收到新消息仍允许发送")
	}
}

// TestNormalizeTaskType_ReGreet 验证 normalizeTaskType 接受 re_greet。
func TestNormalizeTaskType_ReGreet(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "greeting"},
		{"greeting", "greeting"},
		{"auto_reply", "auto_reply"},
		{"re_greet", "re_greet"},
		{"greeting,auto_reply", "greeting"},
		{"auto_reply,re_greet", "auto_reply"},
		{"re_greet,greeting", "re_greet"},
	}
	for _, c := range cases {
		got, err := normalizeTaskType(c.in)
		if err != nil {
			t.Fatalf("normalizeTaskType(%q) 错误：%v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("normalizeTaskType(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	if _, err := normalizeTaskType("unknown"); err == nil {
		t.Fatalf("未知任务类型应报错")
	}
}

// TestParseTaskTypes_ReGreet 验证 parseTaskTypes 把 re_greet 视作有效类型并去重。
func TestParseTaskTypes_ReGreet(t *testing.T) {
	got := parseTaskTypes("greeting,auto_reply,re_greet")
	if len(got) != 3 || !hasTaskType(got, "re_greet") || !hasTaskType(got, "auto_reply") || !hasTaskType(got, "greeting") {
		t.Fatalf("三选期望全部命中，实际：%v", got)
	}
	dedup := parseTaskTypes("re_greet,re_greet,greeting")
	if len(dedup) != 2 {
		t.Fatalf("去重后应为 2，实际：%v", dedup)
	}
	filtered := parseTaskTypes("re_greet,unknown,greeting")
	if len(filtered) != 2 || hasTaskType(filtered, "unknown") {
		t.Fatalf("未知类型应被过滤：%v", filtered)
	}
}

// TestExtractReGreetPrefs_Defaults 验证启动参数全缺时回退到清单默认值。
func TestExtractReGreetPrefs_Defaults(t *testing.T) {
	prefs := extractReGreetPrefs(StartOptions{})
	if prefs.intervalMinMinutes != 30 || prefs.intervalMaxMinutes != 50 || prefs.timeRangeDays != 7 || prefs.maxCount != 1 {
		t.Fatalf("默认值不符合清单：%+v", prefs)
	}
}

// TestExtractReGreetPrefs_Override 验证启动参数覆盖默认值，并修正 max < min 的非法区间。
func TestExtractReGreetPrefs_Override(t *testing.T) {
	prefs := extractReGreetPrefs(StartOptions{
		ReGreetIntervalMin: 60,
		ReGreetIntervalMax: 40, // 小于 min，应被修正为 min
		ReGreetTimeRange:   14,
		ReGreetMaxCount:    3,
	})
	if prefs.intervalMinMinutes != 60 {
		t.Fatalf("intervalMinMinutes 应被覆盖为 60，实际 %d", prefs.intervalMinMinutes)
	}
	if prefs.intervalMaxMinutes != 60 {
		t.Fatalf("intervalMaxMinutes 小于 min 时应被修正为 min，实际 %d", prefs.intervalMaxMinutes)
	}
	if prefs.timeRangeDays != 14 || prefs.maxCount != 3 {
		t.Fatalf("time_range / max_count 应被覆盖：%+v", prefs)
	}
}

// TestRandomInterval_Bounds 验证随机间隔落在 [min, max] 区间内。
func TestRandomInterval_Bounds(t *testing.T) {
	minMinutes := 30
	maxMinutes := 50
	for i := 0; i < 200; i++ {
		d := randomInterval(minMinutes, maxMinutes)
		if d < time.Duration(minMinutes)*time.Minute || d > time.Duration(maxMinutes)*time.Minute {
			t.Fatalf("随机间隔越界：%v", d)
		}
	}
	if randomInterval(10, 10) != 10*time.Minute {
		t.Fatalf("min == max 时应返回固定值")
	}
	if randomInterval(20, 5) != 20*time.Minute {
		t.Fatalf("min > max 时应回退到 min")
	}
}
