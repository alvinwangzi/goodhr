// 本文件将 HRPlus 已清理但云端任务关联不明确的事实加密落盘，恢复只核对和释放原占用，不启动页面。
package planoperations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/protectedsession"
	"reflect"
)

var ErrCleanupQueued = errors.New("页面已清理，原结算意图已保存，等待同账号核对云端任务")

// CleanupIntent 保存实际进度与原领取编号；不猜测释放序号，也不包含当前登录令牌。
type CleanupIntent struct {
	Run              planmodel.Run `json:"run"`
	ClaimRequestID   string        `json:"claim_request_id"`
	ReleaseRequestID string        `json:"release_request_id"`
	State            string        `json:"state"`
	Reason           string        `json:"reason"`
	CleanupConfirmed bool          `json:"cleanup_confirmed"`
}

// StageCleanup 只接纳已证明清理的原运行，先保存密文，再由调用方交还本地父占用。
func (s *Store) StageCleanup(ctx context.Context, scope string, e CleanupIntent) (localdb.PlanCleanupOperation, error) {
	if e.Run.Validate() != nil || !planmodel.ValidID(e.ClaimRequestID) || !e.CleanupConfirmed {
		return localdb.PlanCleanupOperation{}, localdb.ErrPlanRequestConflict
	}
	switch e.State {
	case "blocked", "stopped", "incomplete", "waiting_window":
	default:
		return localdb.PlanCleanupOperation{}, localdb.ErrPlanRequestConflict
	}
	claim, err := s.OriginalClaim(ctx, scope, e.ClaimRequestID)
	if err != nil {
		return localdb.PlanCleanupOperation{}, err
	}
	if claim.RunID != e.Run.ID || claim.PlanID != e.Run.PlanID || claim.OwnerID != e.Run.OwnerID || claim.ActivationID != e.Run.ActivationID || claim.ExpectedVersion != e.Run.ConfigVersion || claim.ExecutionDate != e.Run.ExecutionDate {
		return localdb.PlanCleanupOperation{}, localdb.ErrPlanRequestConflict
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("hrplus/cleanup/"+e.Run.ID+"/"+e.Run.OwnerID)).String()
	e.ReleaseRequestID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("hrplus/cleanup-release/"+id)).String()
	raw, err := json.Marshal(e)
	if err != nil {
		return localdb.PlanCleanupOperation{}, err
	}
	defer clear(raw)
	cipher, err := protectedsession.ProtectExecution(scope, raw)
	if err != nil {
		return localdb.PlanCleanupOperation{}, err
	}
	return s.db.SavePlanCleanup(ctx, localdb.PlanCleanupOperation{OwnerScope: scope, RequestID: id, PlanID: e.Run.PlanID, RunID: e.Run.ID, OwnerID: e.Run.OwnerID, BodyHash: requestHash(raw), Cipher: cipher})
}

// OriginalCleanup 核对原密文、路由及实际清理声明，不允许修改原运行归属。
func (s *Store) OriginalCleanup(ctx context.Context, o localdb.PlanCleanupOperation) (CleanupIntent, error) {
	record, err := s.db.PlanCleanupOperation(ctx, o.OwnerScope, o.RequestID)
	if err != nil {
		return CleanupIntent{}, err
	}
	raw, err := protectedsession.UnprotectExecution(o.OwnerScope, record.Cipher)
	if err != nil {
		return CleanupIntent{}, err
	}
	defer clear(raw)
	if requestHash(raw) != record.BodyHash {
		return CleanupIntent{}, localdb.ErrPlanRequestConflict
	}
	var e CleanupIntent
	if err = json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if e.Run.Validate() != nil || !e.CleanupConfirmed || e.Run.ID != record.RunID || e.Run.PlanID != record.PlanID || e.Run.OwnerID != record.OwnerID || !planmodel.ValidID(e.ReleaseRequestID) {
		return CleanupIntent{}, localdb.ErrPlanRequestConflict
	}
	return e, nil
}

// ResolveNextCleanup 用同账号新授权只读核对原云端关联，生成一次原释放；释放确认后才完成意图。
func (s *Store) ResolveNextCleanup(ctx context.Context, client *cloudapi.Client, a Authority) (bool, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return false, ErrCleanupQueued
	}
	o, err := s.db.NextPlanCleanup(ctx, a.OwnerScope)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e, err := s.OriginalCleanup(ctx, o)
	if err != nil {
		return false, err
	}
	if release, err := s.db.PlanOperation(ctx, a.OwnerScope, e.ReleaseRequestID); err == nil {
		if release.State != "confirmed" {
			return false, nil
		}
		return true, s.db.ConfirmPlanCleanup(ctx, o, e.ReleaseRequestID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	identity, err := client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return false, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != a.OwnerScope || !a.StillCurrent() {
		return false, ErrCleanupQueued
	}
	claim, err := s.OriginalClaim(ctx, a.OwnerScope, e.ClaimRequestID)
	if err != nil {
		return false, err
	}
	if claim.RunID != e.Run.ID || claim.PlanID != e.Run.PlanID || claim.OwnerID != e.Run.OwnerID || claim.ActivationID != e.Run.ActivationID || claim.ExpectedVersion != e.Run.ConfigVersion || claim.ExecutionDate != e.Run.ExecutionDate {
		return false, localdb.ErrPlanRequestConflict
	}
	actual, err := client.ExecutionPlanRun(ctx, a.Token, e.Run.ID)
	if err != nil {
		return false, err
	}
	if actual.ID != e.Run.ID || actual.PlanID != e.Run.PlanID || actual.ActivationID != e.Run.ActivationID || actual.OwnerID != e.Run.OwnerID || actual.ConfigVersion != e.Run.ConfigVersion || actual.ExecutionDate != e.Run.ExecutionDate || !reflect.DeepEqual(actual.Snapshot, e.Run.Snapshot) || len(actual.Items) != len(e.Run.Items) || !a.StillCurrent() {
		return false, localdb.ErrPlanRequestConflict
	}
	for i, fact := range e.Run.Items {
		if actual.Items[i].ID != fact.ID || actual.Items[i].ItemID != fact.ItemID || fact.TaskRunID != "" && actual.Items[i].TaskRunID != fact.TaskRunID {
			return false, localdb.ErrPlanRequestConflict
		}
	}
	if err := s.db.SavePlanRunSnapshot(ctx, a.OwnerScope, actual); err != nil {
		return false, err
	}
	for i, fact := range e.Run.Items {
		for action, progress := range fact.Actions {
			current := actual.Items[i].Actions[action]
			if progress.Count > current.Count {
				current.Count = progress.Count
			}
			if progress.UnknownCount > current.UnknownCount {
				current.UnknownCount = progress.UnknownCount
			}
			actual.Items[i].Actions[action] = current
		}
	}
	input := cloudapi.PlanRunUpdateRequest{RunID: actual.ID, Action: "release", RequestID: e.ReleaseRequestID, OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: actual.Sequence + 1, State: e.State, CurrentItem: actual.CurrentItem, EndReason: e.Reason, CleanupConfirmed: true}
	for _, item := range actual.Items {
		input.Items = append(input.Items, planmodel.ItemUpdate{ID: item.ID, ItemID: item.ItemID, State: item.State, Actions: item.Actions})
	}
	if !a.StillCurrent() {
		return false, ErrCleanupQueued
	}
	_, err = s.StageUpdate(ctx, a.OwnerScope, actual.PlanID, input)
	return err == nil, err
}
