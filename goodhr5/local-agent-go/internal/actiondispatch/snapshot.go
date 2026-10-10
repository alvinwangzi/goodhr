// 本文件保存 HRPlus 消息调度的公平性事实，恢复不产生页面动作、不恢复停止授权。
package actiondispatch

import (
	"fmt"
	"time"
)

// Snapshot 保存原检查时间和批次预算，优先策略仍由当前已核对配置决定。
type Snapshot struct {
	NextCheck      time.Time `json:"next_check"`
	MessageBatches int       `json:"message_batches"`
	LastMessage    Action    `json:"last_message"`
	CycleServed    bool      `json:"cycle_served"`
}

// Snapshot 复制当前公平性值，不包含已停止句柄或页面状态。
func (s *Scheduler) Snapshot() Snapshot {
	return Snapshot{NextCheck: s.NextCheck, MessageBatches: s.messageBatches, LastMessage: s.lastMessage, CycleServed: s.cycleServed}
}

// Restore 核对预算与已知动作；恢复后使用当前时钟做一次到期判断，不补旧周期。
func (s *Scheduler) Restore(value Snapshot) error {
	if value.MessageBatches < 0 {
		return fmt.Errorf("消息批次预算无效")
	}
	if value.LastMessage != "" && value.LastMessage != Reply && value.LastMessage != ReGreet && value.LastMessage != CandidateInfo {
		return fmt.Errorf("原消息动作不支持")
	}
	s.NextCheck = value.NextCheck
	s.messageBatches = value.MessageBatches
	s.lastMessage = value.LastMessage
	s.cycleServed = value.CycleServed
	return nil
}
