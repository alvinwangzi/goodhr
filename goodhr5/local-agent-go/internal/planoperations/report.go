// 本文件补传 HRPlus 已保存原报告，权限变化保留原内容，原回执确认后才标记上传。
package planoperations

import (
	"context"
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
)

// UploadNextReport 用当前已核对的同账号及设备发送原摘要，重试不改写内容或生成时间。
func (s *Store) UploadNextReport(ctx context.Context, client *cloudapi.Client, machineID string, a Authority) (bool, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return false, errors.New("报告补传登录证明已变化")
	}
	o, err := s.db.NextPlanReportUpload(ctx, a.OwnerScope)
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
		return false, errors.New("报告不属于当前已核对账号")
	}
	if _, err := client.UploadExecutionPlanReport(ctx, a.Token, machineID, o.Report, o.SyncState); err != nil {
		return false, err
	}
	if !a.StillCurrent() {
		return false, errors.New("原报告回执已收到，等待同账号重核")
	}
	confirm := func() error { return s.db.ConfirmPlanReportUpload(ctx, o) }
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	return err == nil, err
}
