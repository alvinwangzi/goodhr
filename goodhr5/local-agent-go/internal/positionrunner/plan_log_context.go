// 本文件固定 HRPlus 日志生产者的原执行项归属，迟到回调不读取当前岗位角色，也不保存登录凭证。
package positionrunner

import (
	"context"
	"errors"
	"goodhr5/local-agent-go/internal/localdb"
	"strings"
	"sync"
	"time"
)

// planLogContextKey 只作为进程内原生产者上下文标记，不序列化到运行数据。
type planLogContextKey struct{}

// planLogProducer 保留不可变原归属及第一次保存错误，不包含页面或令牌。
type planLogProducer struct {
	original localdb.PlanItemLog
	mu       sync.Mutex
	failure  error
}

// bindPlanLogContext 在已经核对的原检查点上固定日志来源，子上下文和异步回调继承同一原归属。
func bindPlanLogContext(ctx context.Context, cp localdb.ActionCheckpoint) context.Context {
	producer := &planLogProducer{original: localdb.PlanItemLog{OwnerScope: cp.OwnerScope, PlanRunID: cp.PlanRunID, ItemRunID: cp.ItemRunID, TaskRunID: cp.CloudRunID, LocalRunID: cp.RunID, PositionID: cp.PositionID}}
	return context.WithValue(ctx, planLogContextKey{}, producer)
}

// planLogFailure 在安全步骤边界读取实际日志保存错误，不能把丢失日志的步骤称为已完整记录。
func planLogFailure(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	producer, _ := ctx.Value(planLogContextKey{}).(*planLogProducer)
	if producer == nil {
		return nil
	}
	producer.mu.Lock()
	defer producer.mu.Unlock()
	return producer.failure
}

// positionContextLog 沿用岗位日志并保存有明确来源的原执行项日志，取消后的迟到事实仍保留原归属。
func (r *Runner) positionContextLog(ctx context.Context, positionID, level, message string) {
	positionID, level, message = strings.TrimSpace(positionID), strings.TrimSpace(level), strings.TrimSpace(message)
	if message == "" {
		return
	}
	r.positionLog(positionID, level, message)
	if ctx == nil || r.db == nil {
		return
	}
	producer, _ := ctx.Value(planLogContextKey{}).(*planLogProducer)
	if producer == nil {
		return
	}
	var err error
	if producer.original.PositionID != positionID {
		err = errors.New("日志生产者岗位与原执行项不一致")
	} else {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = r.db.AddPlanItemLog(writeCtx, producer.original.OwnerScope, producer.original.LocalRunID, level, message, producer.original)
		cancel()
	}
	if err != nil {
		producer.mu.Lock()
		if producer.failure == nil {
			producer.failure = err
		}
		producer.mu.Unlock()
	}
}
