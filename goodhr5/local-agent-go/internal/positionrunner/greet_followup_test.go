// 本文件用真实 SQLite 与明确的会话 ID 边界验证追加问候，不把同名目标或未确认结果作为成功。
package positionrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// followupFixture 只替换页面边界，追加发送与状态记录使用实际主流程。
type followupFixture struct {
	platformcore.Runtime
	*reGreetFixture
	returned int
}

// PrepareCandidateFollowup 表示已安全进入可核对 ID 的消息页。
func (f *followupFixture) PrepareCandidateFollowup(context.Context, platformcore.Executor) error {
	return nil
}

// CaptureRecommendationCursor 返回明确的局部锚点。
func (f *followupFixture) CaptureRecommendationCursor(_ context.Context, _ platformcore.Executor, anchors []string) (platformcore.RecommendationCursor, error) {
	return platformcore.RecommendationCursor{Valid: true, Anchors: anchors}, nil
}

// ReturnToRecommendation 记录恢复页面次数。
func (f *followupFixture) ReturnToRecommendation(context.Context, platformcore.Executor) error {
	f.returned++
	return nil
}

// CheckRecommendationCursor 表示局部锚点仍匹配。
func (f *followupFixture) CheckRecommendationCursor(context.Context, platformcore.Executor, platformcore.RecommendationCursor) (bool, string, error) {
	return true, "resume_anchor_match", nil
}

// RewindRecommendation 保留回退接口，正常路径不会调用。
func (f *followupFixture) RewindRecommendation(context.Context, platformcore.Executor) error {
	return nil
}

// TestVerifiedGreetFollowup 验证明确 ID 发送一次、重复调用不重发，错误 ID 不发送。
func TestVerifiedGreetFollowup(t *testing.T) {
	for _, correct := range []bool{true, false} {
		t.Run(map[bool]string{true: "正确ID", false: "同名错误ID"}[correct], func(t *testing.T) {
			db := openRunnerTestDB(t)
			runner := newTestRunner(t, db, &onceWorker{})
			position := localdb.Position{ID: "p", PlatformID: "boss", Name: "Go", PositionSnapshot: map[string]any{"name": "Go"}}
			cp, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: "p", ProfileScope: "legacy", Platform: "boss"})
			if err != nil {
				t.Fatal(err)
			}
			cp, err = db.BindActionRunAccount(t.Context(), cp.RunID, "scope", "owner")
			if err != nil {
				t.Fatal(err)
			}
			mapped := "c1"
			if !correct {
				mapped = "wrong"
			}
			if err = db.SaveCandidateIdentity(t.Context(), localdb.CandidateIdentity{ProfileScope: "scope", Platform: "boss", RecommendationID: "opaque", ConversationID: mapped, Status: "verified", Source: "controlled-direct-id"}); err != nil {
				t.Fatal(err)
			}
			fixture := &followupFixture{reGreetFixture: &reGreetFixture{replyFixture: &replyFixture{}, confirmed: true, current: platformcore.ReplyContext{ResumeStatus: "none", Conversation: platformcore.ReplyConversation{ID: "c1", Name: "张三", PositionName: "Go"}, Messages: []platformcore.ReplyMessage{{Direction: "outbound", Kind: "text", Text: "首次招呼"}}}}}
			candidate := map[string]any{"id": "opaque", "candidate_name": "张三"}
			options := StartOptions{LocalRunID: cp.RunID, CloudRunID: "task"}
			exec := platformExecutor{runner: runner, positionID: "p"}
			err = runner.sendVerifiedGreetFollowup(t.Context(), position, options, fixture, exec, cloudapi.PlatformConfig{}, candidate, "岗位追加内容")
			if correct {
				if err != nil || fixture.sent != 1 {
					t.Fatalf("追加发送失败 sent=%d err=%v", fixture.sent, err)
				}
				if err = runner.sendVerifiedGreetFollowup(t.Context(), position, options, fixture, exec, cloudapi.PlatformConfig{}, candidate, "岗位追加内容"); err == nil || fixture.sent != 1 {
					t.Fatal("重复追加被再次发送")
				}
			} else if err == nil || fixture.sent != 0 {
				t.Fatal("同名错误目标被发送")
			}
			if fixture.returned == 0 {
				t.Fatal("追加流程没有恢复推荐页面")
			}
		})
	}
}
