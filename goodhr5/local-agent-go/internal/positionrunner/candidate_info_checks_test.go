// 本文件验证公共索要编排的勾选过滤、发送前落库、独立结果和未知状态防重，不访问招聘网站。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// infoCheckFixture 替换页面边界，队列和公共流程使用真实 SQLite 实现。
type infoCheckFixture struct {
	platformcore.Runtime
	*reGreetFixture
	prepared, submitted []string
	beforeSubmit        func(string)
	legacyCalls         int
	identityIDs         []string
}

// LocateReplyConversationByID 记录预期 ID，同名候选人只按已验证映射定位。
func (f *infoCheckFixture) LocateReplyConversationByID(_ context.Context, _ platformcore.Executor, name, id string) (platformcore.ReplyConversation, error) {
	f.identityIDs = append(f.identityIDs, id)
	f.current.Conversation = platformcore.ReplyConversation{ID: id, Name: name, PositionName: "Go"}
	return f.current.Conversation, nil
}

// PrepareCandidateInfoRequest 记录每个准备动作，模拟同一候选人的正确确认框。
func (f *infoCheckFixture) PrepareCandidateInfoRequest(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, _ platformcore.ReplyConversation, action string) (platformcore.CandidateInfoPreparation, error) {
	f.prepared = append(f.prepared, action)
	return platformcore.CandidateInfoPreparation{Action: action}, nil
}

// SubmitCandidateInfoRequest 模拟电话成功、微信未确认，验证两项结果不相互覆盖。
func (f *infoCheckFixture) SubmitCandidateInfoRequest(_ context.Context, _ platformcore.Executor, _ platformcore.ReplyTarget, _ platformcore.ReplyConversation, p platformcore.CandidateInfoPreparation) (string, error) {
	if f.beforeSubmit != nil {
		f.beforeSubmit(p.Action)
	}
	f.submitted = append(f.submitted, p.Action)
	if p.Action == "wechat" {
		return "unknown", fmt.Errorf("模拟结果未确认")
	}
	return "requested", nil
}

// CancelCandidateInfoRequest 测试中没有真实弹窗，取消不产生业务副作用。
func (f *infoCheckFixture) CancelCandidateInfoRequest(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, platformcore.CandidateInfoPreparation) error {
	return nil
}

// InspectCandidateInfoRequest 未取得新证据时继续保持未知，不允许再次提交。
func (f *infoCheckFixture) InspectCandidateInfoRequest(context.Context, platformcore.Executor, platformcore.ReplyTarget, platformcore.ReplyConversation, string) (bool, error) {
	return false, nil
}

// CheckResumeRequests 记录历史名单调用，确保仅勾电话不会进入旧的简历分支。
func (f *infoCheckFixture) CheckResumeRequests(context.Context, platformcore.Executor, cloudapi.PlatformConfig, []string) (map[string]platformcore.ResumeRequestOutcome, error) {
	f.legacyCalls++
	return nil, nil
}

// TestCandidateInfoChecksIndependentActions 验证电话与微信分别记录，只勾两项不求简历，重跑不再次提交未知请求。
func TestCandidateInfoChecksIndependentActions(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "subscription": map[string]any{"active": true, "allow_auto_reply": true}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "saved": 1})
	}))
	defer cloud.Close()
	r, db := newTestRunnerWithDB(t, &onceWorker{})
	position, err := db.CreatePosition(map[string]any{"name": "Go", "platform_id": "boss"})
	if err != nil {
		t.Fatal(err)
	}
	position.PositionSnapshot = map[string]any{"common_config": map[string]any{"request_phone": true, "request_wechat": true, "request_resume": false}}
	item, err := db.EnqueueCandidateInfoRequest(position.ID, "boss", "stable", "候选人", map[string]bool{"phone": true, "wechat": true})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.EnqueueResumeRequest(position.ID, "boss", "旧简历名单")
	f := &infoCheckFixture{reGreetFixture: &reGreetFixture{replyFixture: &replyFixture{}, current: platformcore.ReplyContext{Conversation: platformcore.ReplyConversation{Name: "候选人"}, Messages: []platformcore.ReplyMessage{{Direction: "inbound", Kind: "text", Text: "你好"}}}}}
	f.beforeSubmit = func(action string) {
		items, err := db.ListCandidateInfoRequests(position.ID)
		if err != nil || items[0].Results[action] != "sending" {
			t.Fatal("点击确认之前没有持久化发送意图")
		}
	}
	options := StartOptions{CloudAPIBase: cloud.URL, Token: "test"}
	r.performResumeChecks(t.Context(), position, f, nil, options)
	if !reflect.DeepEqual(f.submitted, []string{"phone", "wechat"}) || f.legacyCalls != 0 {
		t.Fatalf("动作选择错误：%v legacy=%d", f.submitted, f.legacyCalls)
	}
	items, err := db.ListCandidateInfoRequests(position.ID)
	if err != nil || items[0].ID != item.ID || items[0].Results["phone"] != "requested" || items[0].Results["wechat"] != "unknown" {
		t.Fatalf("独立结果未保存：%+v %v", items, err)
	}
	r.performResumeChecks(t.Context(), position, f, nil, options)
	if len(f.submitted) != 2 {
		t.Fatalf("重新检查重复提交请求：%v", f.submitted)
	}
}

// TestM1CandidateInfoSameNamesAndBudget 验证两个同名对象使用各自 ID，首人耗时过长后交还第二人，停止后不另起页面循环。
func TestM1CandidateInfoSameNamesAndBudget(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"subscription": map[string]any{"active": true, "allow_auto_reply": true}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	defer cloud.Close()
	r, db := newTestRunnerWithDB(t, &onceWorker{})
	position, err := db.CreatePosition(map[string]any{"name": "Go", "platform_id": "boss"})
	if err != nil {
		t.Fatal(err)
	}
	position.PositionSnapshot = map[string]any{"common_config": map[string]any{"request_phone": true, "request_resume": false}}
	ids := []string{}
	for _, candidateID := range []string{"geek-A", "geek-B"} {
		item, err := db.EnqueueCandidateInfoRequest(position.ID, "boss", candidateID, "同名", map[string]bool{"phone": true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
		if err = db.SaveCandidateIdentity(t.Context(), localdb.CandidateIdentity{ProfileScope: platformcore.ReplyHash("profile:default"), Platform: "boss", RecommendationID: candidateID, ConversationID: "conversation-" + candidateID, Source: "controlled-direct-transition", Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &infoCheckFixture{reGreetFixture: &reGreetFixture{replyFixture: &replyFixture{}, current: platformcore.ReplyContext{Messages: []platformcore.ReplyMessage{{Direction: "inbound", Kind: "text", Text: "你好"}}}}}
	now := time.Now()
	f.beforeSubmit = func(string) { now = now.Add(61 * time.Second) }
	remaining := []string{}
	checkpoint, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: position.ID, Platform: "boss", ProfileScope: platformcore.ReplyHash("profile:default"), CloudRunID: "fixture-original-task"})
	if err != nil {
		t.Fatal(err)
	}
	options := StartOptions{LocalRunID: checkpoint.RunID, CloudRunID: checkpoint.CloudRunID, CloudAPIBase: cloud.URL, Token: "fixture", candidateInfoBatchIDs: ids, candidateInfoRemaining: &remaining, actionNow: func() time.Time { return now }}
	r.performCandidateInfoChecks(t.Context(), position, f, options)
	if len(f.submitted) != 1 || len(remaining) != 1 || len(f.identityIDs) != 1 {
		t.Fatalf("预算耗尽未保留队列：submitted=%v remaining=%v ids=%v", f.submitted, remaining, f.identityIDs)
	}
	options.candidateInfoBatchIDs = remaining
	f.beforeSubmit = nil
	r.performCandidateInfoChecks(t.Context(), position, f, options)
	if len(f.submitted) != 2 || reflect.DeepEqual(f.identityIDs[:1], f.identityIDs[1:]) {
		t.Fatalf("同名没有按不同 ID：%v", f.identityIDs)
	}
	r.asyncCheckResumeRequests(position, nil, options)
	if f.legacyCalls != 0 {
		t.Fatal("M1 仍调用历史姓名名单")
	}
}
