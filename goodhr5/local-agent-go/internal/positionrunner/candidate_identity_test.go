// 本文件验证页面映射必须重新核对，旧账号映射和冲突不能退回姓名或沿用旧身份。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// identityResolverFixture 返回虚构的当前页面直接核对结果。
type identityResolverFixture struct {
	id  string
	err error
}

// ResolveCandidateConversationID 模拟仅提供 ID 证据的平台能力。
func (f identityResolverFixture) ResolveCandidateConversationID(context.Context, platformcore.Executor, string, string) (string, string, error) {
	return f.id, "controlled_response_and_chat_id", f.err
}

// TestCandidateIdentityRevalidation 验证旧 verified 映射不绕过当前页面核对，冲突保持隔离。
func TestCandidateIdentityRevalidation(t *testing.T) {
	db := openRunnerTestDB(t)
	runner := newTestRunner(t, db, &onceWorker{})
	exec := platformExecutor{runner: runner}
	identity, err := runner.verifiedCandidateIdentity(t.Context(), exec, identityResolverFixture{id: "123-7"}, "scope", "boss", "opaque-A")
	if err != nil || identity.Status != "verified" || identity.ConversationID != "123-7" {
		t.Fatalf("未记录页面证据：%+v %v", identity, err)
	}
	identity, err = runner.verifiedCandidateIdentity(t.Context(), exec, identityResolverFixture{err: platformcore.ErrReplyUnsafe}, "scope", "boss", "opaque-A")
	if !errors.Is(err, platformcore.ErrReplyUnsafe) || identity.Status == "verified" {
		t.Fatal("当前页面核对失败后沿用了旧映射")
	}
	_, err = runner.verifiedCandidateIdentity(t.Context(), exec, identityResolverFixture{id: "124-0"}, "scope", "boss", "opaque-A")
	if !errors.Is(err, localdb.ErrIdentityConflict) {
		t.Fatal("不同数字 ID 覆盖旧映射")
	}
	stored, err := db.CandidateIdentityFor(t.Context(), "scope", "boss", "opaque-A")
	if err != nil || stored.Status != "conflict" {
		t.Fatal("冲突未持久保存")
	}
}
