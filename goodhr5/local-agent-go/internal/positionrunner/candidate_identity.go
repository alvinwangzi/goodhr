// 本文件复用候选人身份库，并在支持的平台上用当前页面事实重新核对跨入口映射，不依据姓名或旧记录猜测。
package positionrunner

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// verifiedCandidateIdentity 在每次索要或复打之前重新核对页面映射；姓名仅用于发现入口，冲突不能绕过 ID 核对。
func (r *Runner) verifiedCandidateIdentity(ctx context.Context, exec platformcore.Executor, runtime any, scope, platform, recommendationID string, candidateName ...string) (localdb.CandidateIdentity, error) {
	identity, err := r.db.CandidateIdentityFor(ctx, scope, platform, recommendationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return identity, err
	}
	if identity.Status == "conflict" {
		return identity, localdb.ErrIdentityConflict
	}
	resolver, supported := runtime.(platformcore.CandidateIdentityResolver)
	if !supported {
		return identity, err
	}
	name := ""
	if len(candidateName) > 0 {
		name = candidateName[0]
	}
	id, source, resolveErr := resolver.ResolveCandidateConversationID(ctx, exec, recommendationID, name)
	if resolveErr != nil {
		return localdb.CandidateIdentity{}, resolveErr
	}
	identity = localdb.CandidateIdentity{ProfileScope: scope, Platform: platform, RecommendationID: recommendationID, ConversationID: id, Source: source, Status: "verified"}
	if saveErr := r.db.SaveCandidateIdentity(ctx, identity); saveErr != nil {
		return localdb.CandidateIdentity{}, saveErr
	}
	return identity, nil
}
