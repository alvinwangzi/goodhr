// 本文件加密保存 HRPlus 原停止确认并可靠补传，原释放未确认时不声明计划收尾完成。
package planoperations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/protectedsession"
	"reflect"
)

// stopEnvelope 将原计划快照与请求一起加密，回执不能借用其他版本或启用批次。
type stopEnvelope struct {
	Plan    planmodel.Plan           `json:"plan"`
	Request cloudapi.PlanStopRequest `json:"request"`
}

// StageStop 在本地原运行已经收尾后登记一次确认，原随机编号、内容和落盘顺序保留至回执确认。
func (s *Store) StageStop(ctx context.Context, base, scope string, p planmodel.Plan) (localdb.PlanStopOperation, error) {
	if err := p.Validate(); err != nil {
		return localdb.PlanStopOperation{}, err
	}
	if p.State != "stopped" || !p.StopRequested || cloudapi.SessionOwnerScope(base, p.UserEmail) != scope {
		return localdb.PlanStopOperation{}, localdb.ErrPlanRequestConflict
	}
	ready, err := s.db.PlanStopReady(ctx, scope, p.ID)
	if err != nil {
		return localdb.PlanStopOperation{}, err
	}
	if !ready {
		return localdb.PlanStopOperation{}, errors.New("原计划运行或释放尚未确认，不能登记停止确认")
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("hrplus/stop/%s/%s/%d/%s", p.ID, p.ActivationID, p.Version, p.MachineID))).String()
	if old, err := s.db.PlanStopOperation(ctx, scope, id); err == nil {
		original, err := s.OriginalStop(ctx, old)
		if err != nil {
			return localdb.PlanStopOperation{}, err
		}
		if original.Plan.UserEmail != p.UserEmail || original.Plan.TenantID != p.TenantID || !reflect.DeepEqual(original.Plan.Config, p.Config) || !original.Plan.CreatedAt.Equal(p.CreatedAt) {
			return localdb.PlanStopOperation{}, localdb.ErrPlanRequestConflict
		}
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return localdb.PlanStopOperation{}, err
	}
	if err := s.db.SaveCachedPlan(ctx, scope, p); err != nil {
		return localdb.PlanStopOperation{}, err
	}
	e := stopEnvelope{Plan: p, Request: cloudapi.PlanStopRequest{RequestID: id, ExpectedVersion: p.Version, ActivationID: p.ActivationID, MachineID: p.MachineID, CleanupConfirmed: true}}
	raw, err := json.Marshal(e)
	if err != nil {
		return localdb.PlanStopOperation{}, err
	}
	defer clear(raw)
	cipher, err := protectedsession.ProtectExecution(scope, raw)
	if err != nil {
		return localdb.PlanStopOperation{}, err
	}
	return s.db.SavePlanStop(ctx, localdb.PlanStopOperation{OwnerScope: scope, RequestID: id, PlanID: p.ID, ActivationID: p.ActivationID, Version: p.Version, MachineID: p.MachineID, BodyHash: requestHash(raw), Cipher: cipher})
}

// OriginalStop 从原密文核对全部路由和收尾字段，不把当前网页状态拼成新请求。
func (s *Store) OriginalStop(ctx context.Context, o localdb.PlanStopOperation) (stopEnvelope, error) {
	record, err := s.db.PlanStopOperation(ctx, o.OwnerScope, o.RequestID)
	if err != nil {
		return stopEnvelope{}, err
	}
	raw, err := protectedsession.UnprotectExecution(o.OwnerScope, record.Cipher)
	if err != nil {
		return stopEnvelope{}, err
	}
	defer clear(raw)
	if requestHash(raw) != record.BodyHash {
		return stopEnvelope{}, localdb.ErrPlanRequestConflict
	}
	var e stopEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if e.Plan.Validate() != nil || e.Request.Validate() != nil || e.Plan.ID != record.PlanID || e.Plan.ActivationID != record.ActivationID || e.Plan.Version != record.Version || e.Plan.MachineID != record.MachineID || e.Plan.State != "stopped" || !e.Plan.StopRequested || e.Request.RequestID != record.RequestID || e.Request.ExpectedVersion != record.Version || e.Request.ActivationID != record.ActivationID || e.Request.MachineID != record.MachineID {
		return stopEnvelope{}, localdb.ErrPlanRequestConflict
	}
	return e, nil
}

// UploadNextStop 先核对当前真实账号和原释放状态，再发送原确认；授权变化保留原回执等待重核。
func (s *Store) UploadNextStop(ctx context.Context, client *cloudapi.Client, a Authority) (bool, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return false, errors.New("停止确认登录证明已变化")
	}
	o, err := s.db.NextPlanStop(ctx, a.OwnerScope)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ready, err := s.db.PlanStopReady(ctx, a.OwnerScope, o.PlanID)
	if err != nil {
		return false, err
	}
	if !ready {
		return false, nil
	}
	identity, err := client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return false, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != a.OwnerScope || !a.StillCurrent() {
		return false, errors.New("停止确认不属于当前登录账号")
	}
	e, err := s.OriginalStop(ctx, o)
	if err != nil {
		return false, err
	}
	if e.Plan.TenantID != "" && e.Plan.TenantID != identity.TenantID {
		return false, errors.New("停止确认团队已变化")
	}
	if !a.StillCurrent() {
		return false, errors.New("停止确认登录证明已变化")
	}
	p, err := client.ConfirmExecutionPlanStop(ctx, a.Token, e.Plan, e.Request)
	if err != nil {
		return false, err
	}
	if !a.StillCurrent() {
		return false, errors.New("原停止回执已收到，等待同账号核对")
	}
	confirm := func() error { return s.db.ConfirmPlanStopSnapshot(ctx, o, p) }
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	return err == nil, err
}
