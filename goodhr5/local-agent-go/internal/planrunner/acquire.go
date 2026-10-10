// 本文件连接 HRPlus 计划名义时段、本地预留、加密原请求与云端许可；领取不直接执行招聘页面动作。
package planrunner

import (
	"context"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/positionrunner"
	"time"
)

var ErrOutsidePlanWindow = errors.New("当前不在计划名义时间段内，等待下一执行时间")
var ErrPlanAuthority = errors.New("计划登录、设备或启用批次已变化")
var ErrPlanNeedsSettlement = errors.New("原运行正在收尾或已结束，只能核对结算，不能重新准备页面")

// Coordinator 只协调领取，复用既有 Runner 预留与云端客户端，不把配置缓存当成启动许可。
type Coordinator struct {
	db       *localdb.DB
	runner   *positionrunner.Runner
	client   *cloudapi.Client
	requests *planoperations.Store
	now      func() time.Time
}

// New 创建计划领取协调器，传入时钟便于精确验证名义结束边界。
func New(db *localdb.DB, runner *positionrunner.Runner, client *cloudapi.Client, now func() time.Time) *Coordinator {
	if now == nil {
		now = time.Now
	}
	return &Coordinator{db: db, runner: runner, client: client, requests: planoperations.New(db), now: now}
}

// Acquired 同时持有当前本地预留和已核对的原云端许可，后续仍需招聘账号及页面准备确认。
type Acquired struct {
	Reservation    *positionrunner.PlanBrowserReservation
	Permit         planmodel.Permit
	canPrepare     func() bool
	scope          string
	claimRequestID string
}

// CanPrepare 确认本进程仍有预留、原登录有效且在名义时段内，不把已收到许可当作永久页面授权。
func (a *Acquired) CanPrepare() bool {
	return a != nil && a.Reservation != nil && a.canPrepare != nil && a.canPrepare()
}

// checkWindow 只接受名义窗口内且原执行日期匹配的请求，随机收尾不提供新启动资格。
func (c *Coordinator) checkWindow(plan planmodel.Plan, input cloudapi.PlanClaimRequest) error {
	interval, err := plan.Config.Schedule.Current(c.now())
	if err != nil {
		return err
	}
	if interval == nil || interval.Date != input.ExecutionDate {
		return ErrOutsidePlanWindow
	}
	return nil
}

// Acquire 先取得本地预留并加密保存原事实，再领取云端；任何失败均不打开页面、不重新生成编号。
func (c *Coordinator) Acquire(ctx context.Context, plan planmodel.Plan, input cloudapi.PlanClaimRequest, a planoperations.Authority) (*Acquired, error) {
	// 冻结编排切片，调用者后续修改缓存不能延长已领取的名义时段。
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var frozen planmodel.Plan
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	plan = frozen
	// 此字段只在真实预留之后落盘和发送，不能把调用方的历史布尔值当成当前证明。
	input.LocalReserved = true
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if a.StillCurrent == nil || !a.StillCurrent() || plan.State != "enabled" || plan.StopRequested || plan.ID != input.PlanID || plan.ActivationID != input.ActivationID || plan.Version != input.ExpectedVersion || plan.MachineID != input.MachineID {
		return nil, ErrPlanAuthority
	}
	if err := c.checkWindow(plan, input); err != nil {
		return nil, err
	}
	identity, err := c.client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return nil, err
	}
	scope := cloudapi.SessionOwnerScope(c.client.BaseURL, identity.UserEmail)
	if scope != a.OwnerScope || identity.UserEmail != plan.UserEmail || (plan.TenantID != "" && identity.TenantID != plan.TenantID) || !a.StillCurrent() {
		return nil, ErrPlanAuthority
	}
	if err = c.checkWindow(plan, input); err != nil {
		return nil, err
	}
	reservation, err := c.runner.ReservePlanBrowser(ctx, input.RunID)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = reservation.Release(true)
		}
	}() // 尚未打开页面，失败只释放本地预留；云端不明确许可由原请求核对。
	input.LocalReserved = true
	operation, err := c.requests.StageClaim(ctx, scope, input)
	if err != nil {
		return nil, err
	}
	original, err := c.requests.OriginalClaim(ctx, scope, input.RequestID)
	if err != nil {
		return nil, err
	}
	if !reservation.Valid() || !a.StillCurrent() {
		return nil, ErrPlanAuthority
	}
	if err = c.checkWindow(plan, original); err != nil {
		return nil, err
	}
	permit, err := c.client.ClaimExecutionPlanRun(reservation.Context(), a.Token, original)
	if err != nil {
		return nil, err
	}
	if !reservation.Valid() || !a.StillCurrent() {
		return nil, ErrPlanAuthority
	}
	confirm := func() error {
		if !reservation.Valid() || !a.StillCurrent() {
			return ErrPlanAuthority
		}
		return c.db.ConfirmPlanOperationSnapshot(ctx, scope, operation.RequestID, operation.BodyHash, permit.Run)
	}
	// 登录锁内只做保存；该锁由调用方提供，内部不能再次调用它的 StillCurrent。
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(func() error {
			if !reservation.Valid() {
				return ErrPlanAuthority
			}
			return c.db.ConfirmPlanOperationSnapshot(ctx, scope, operation.RequestID, operation.BodyHash, permit.Run)
		})
	} else {
		err = confirm()
	}
	if err != nil {
		return nil, err
	}
	if (permit.Run.State != "starting" && permit.Run.State != "running") || (permit.Owner.State != "starting" && permit.Owner.State != "running") {
		return &Acquired{Permit: permit, scope: scope, claimRequestID: input.RequestID}, ErrPlanNeedsSettlement
	}
	if err = c.checkWindow(plan, original); err != nil {
		// 已取得的云端占用不能忘掉；此结果只允许结算，不能准备页面。
		return &Acquired{Permit: permit, scope: scope, claimRequestID: input.RequestID}, err
	}
	accepted = true
	result := &Acquired{Reservation: reservation, Permit: permit, scope: scope, claimRequestID: input.RequestID}
	result.canPrepare = func() bool { return reservation.Valid() && a.StillCurrent() && c.checkWindow(plan, original) == nil }
	return result, nil
}
