// 本文件把 HRPlus 原执行项准备接到 M1 配置、检查点和父页面占用，尚不自动发起页面动作。
package planrunner

import (
	"context"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/planoperations"
	"goodhr5/local-agent-go/internal/positionrunner"
)

// PreparedItem 保留原许可及当前进程的 M1 准备结果，含令牌的配置不序列化、不输出到日志。
type PreparedItem struct {
	Permit      planmodel.Permit                       `json:"permit"`
	Reservation *positionrunner.PlanItemReservation    `json:"-"`
	Snapshot    positionrunner.PositionRuntimeSnapshot `json:"-"`
}

// String 隐藏配置中的当前登录令牌和 AI 凭证。
func (p PreparedItem) String() string { return "HRPlus 已准备执行项（运行配置隐藏）" }

// GoString 隐藏调试输出中的运行配置。
func (p PreparedItem) GoString() string { return p.String() }

// BeginItem 先用原请求取得独立任务，再复用 M1 准备与父引用；失败保留已发生许可供原占用结算。
func (c *Coordinator) BeginItem(ctx context.Context, held *Acquired, run planmodel.Run, requestID string, a planoperations.Authority, options positionrunner.StartOptions) (PreparedItem, error) {
	permit, err := c.PrepareItem(ctx, held, run, requestID, a)
	result := PreparedItem{Permit: permit}
	if err != nil {
		return result, err
	}
	options.Token = a.Token
	options.CloudAPIBase = c.client.BaseURL
	options.MachineID = permit.Owner.MachineID
	child, snapshot, err := c.runner.BeginPlanItem(ctx, held.Reservation, held.scope, permit, options, func() bool { return held.CanPrepare() && a.StillCurrent() })
	if err != nil {
		return result, err
	}
	if !held.CanPrepare() || !a.StillCurrent() {
		// 配置准备没有操作页面，安全交还子引用；父占用仍由原释放流程结算。
		if err = child.ReleaseAfterCleanup(true); err != nil {
			result.Reservation, result.Snapshot = child, snapshot
			return result, err
		}
		return result, ErrPlanNeedsSettlement
	}
	result.Reservation, result.Snapshot = child, snapshot
	return result, nil
}
