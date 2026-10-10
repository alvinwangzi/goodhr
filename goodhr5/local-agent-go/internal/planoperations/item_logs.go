// 本文件补传 HRPlus 原归属日志，登录变化和未知回执不确认，原记录不删除。
package planoperations

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
)

// UploadNextItemLogs 在当前已核对账号下传送一批原日志，完整回执后按原来源确认。
func (s *Store) UploadNextItemLogs(ctx context.Context, client *cloudapi.Client, machine string, a Authority) (bool, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return false, errors.New("日志补传登录证明已变化")
	}
	logs, err := s.db.NextPlanItemLogBatch(ctx, a.OwnerScope)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	identity, err := client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return false, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != a.OwnerScope || !a.StillCurrent() {
		return false, errors.New("原日志不属于当前核对账号")
	}
	if err := client.UploadPlanItemLogs(ctx, a.Token, machine, logs); err != nil {
		return false, err
	}
	if !a.StillCurrent() {
		return false, errors.New("日志原回执保留，等待同账号重新核对")
	}
	hashes := []string{}
	for _, entry := range logs {
		hashes = append(hashes, cloudapi.PlanItemLogHash(entry))
	}
	confirm := func() error { return s.db.ConfirmPlanItemLogs(ctx, a.OwnerScope, logs, hashes) }
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	return err == nil, err
}
