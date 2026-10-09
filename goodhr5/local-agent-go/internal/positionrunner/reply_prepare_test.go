// 本文件验证 HRPlus 同一消息阶段复用已确认岗位，变化和读取错误时保持安全准备边界。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// preparedTargetFixture 记录只读检查与修改页面的次数，不运行真实浏览器或候选人任务。
type preparedTargetFixture struct {
	*replyFixture
	matched                    bool
	checkErr                   error
	checks, prepares, resolves int
}

// CheckReplyTarget 返回当前页面的受控事实。
func (f *preparedTargetFixture) CheckReplyTarget(context.Context, platformcore.Executor, platformcore.ReplyTarget) (bool, error) {
	f.checks++
	return f.matched, f.checkErr
}

// PrepareReplyPage 记录实际页面准备次数。
func (f *preparedTargetFixture) PrepareReplyPage(context.Context, platformcore.Executor) error {
	f.prepares++
	return nil
}

// ResolveReplyTarget 记录实际岗位选择次数。
func (f *preparedTargetFixture) ResolveReplyTarget(_ context.Context, _ platformcore.Executor, name string) (platformcore.ReplyTarget, error) {
	f.resolves++
	return platformcore.ReplyTarget{PositionID: name, PositionName: name, NameUnique: true}, nil
}

// TestPreparedTargetReuse 验证反复进入同一消息阶段不重选，变更才准备，错误不触发额外操作。
func TestPreparedTargetReuse(t *testing.T) {
	f := &preparedTargetFixture{replyFixture: &replyFixture{}, matched: true}
	target := platformcore.ReplyTarget{PositionID: "Go", PositionName: "Go", NameUnique: true}
	for i := 0; i < 4; i++ {
		if _, err := ensureReplyTarget(t.Context(), f, nil, "Go", &target); err != nil {
			t.Fatal(err)
		}
	}
	if f.checks != 4 || f.prepares != 0 || f.resolves != 0 {
		t.Fatalf("同阶段重复准备 %+v", f)
	}
	f.matched = false
	if _, err := ensureReplyTarget(t.Context(), f, nil, "Go", &target); err != nil || f.prepares != 1 || f.resolves != 1 {
		t.Fatalf("变化后没有重新准备 %+v %v", f, err)
	}
	f.checkErr = context.Canceled
	if _, err := ensureReplyTarget(t.Context(), f, nil, "Go", &target); !errors.Is(err, context.Canceled) || f.prepares != 1 {
		t.Fatal("停止或读取错误仍修改页面")
	}
	f.checkErr = nil
	f.matched = true
	if _, err := ensureReplyTarget(t.Context(), f, nil, "Other", &target); err != nil || f.resolves != 2 {
		t.Fatal("另一个岗位复用了旧上下文")
	}
}
