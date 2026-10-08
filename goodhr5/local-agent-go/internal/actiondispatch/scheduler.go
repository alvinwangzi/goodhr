// Package actiondispatch 提供 HRPlus 单岗位三动作的安全调度，只安排动作，不操作招聘页面。
package actiondispatch

import "time"

// Action 是下一次可在候选人安全边界领取的动作。
type Action string

const (
	Greeting      Action = "greeting"
	Reply         Action = "auto_reply"
	ReGreet       Action = "re_greet"
	CheckMessages Action = "check_messages"
	Done          Action = "done"
)

// Work 表示当前已经发现的工作，未来未到期复打不算待处理工作。
type Work struct {
	Greeting, Reply, ReGreet bool
	ReGreetDue               time.Time
	ReGreetWaitingSince      time.Time // 进入本次活跃队列的时间，停机期间不算调度等待。
}

// Scheduler 保存公平性与消息检查时间；调用方只在安全边界调用 Next。
type Scheduler struct {
	PrioritizeReply bool
	Interval        time.Duration
	NextCheck       time.Time
	messageBatches  int
	lastMessage     Action
	cycleServed     bool
	stopped         bool
}

// New 创建首次先检查消息的单岗位调度器，第一版默认每 60 秒检查。
func New(prioritizeReply bool) *Scheduler {
	return &Scheduler{PrioritizeReply: prioritizeReply, Interval: 60 * time.Second}
}

// Stop 禁止领取新动作，已领取动作由执行器完成必要结果确认。
func (s *Scheduler) Stop() { s.stopped = true }

// Checked 在检查完当前消息后记录下一次检查点，定时器本身不进入页面。
func (s *Scheduler) Checked(now time.Time) { s.NextCheck = now.Add(s.Interval); s.cycleServed = false }

// Completed 在批次或一个找简历安全单元结束后更新公平性。
func (s *Scheduler) Completed(action Action) {
	if action == Greeting {
		if s.messageBatches > 0 {
			s.cycleServed = true
		}
		s.messageBatches = 0
		return
	}
	if action == Reply || action == ReGreet {
		s.messageBatches++
		s.lastMessage = action
	}
}

// Next 选择安全边界后的动作；持续消息不能阻塞扫描，到期复打不能被回复饿死。
func (s *Scheduler) Next(now time.Time, w Work) Action {
	if s.stopped {
		return Done
	}
	if s.NextCheck.IsZero() || !now.Before(s.NextCheck) {
		return CheckMessages
	}
	if !s.PrioritizeReply && w.Greeting && s.cycleServed {
		return Greeting
	}
	if w.Greeting && s.messageBatches >= 2 {
		return Greeting
	}
	waitingSince := w.ReGreetWaitingSince
	if waitingSince.IsZero() {
		waitingSince = w.ReGreetDue
	}
	overdue := w.ReGreet && !waitingSince.IsZero() && !now.Before(waitingSince.Add(s.Interval))
	if overdue && (s.lastMessage != ReGreet || !w.Reply) {
		return ReGreet
	}
	if s.PrioritizeReply && w.Reply {
		return Reply
	}
	if w.Greeting && !w.Reply && !w.ReGreet {
		return Greeting
	}
	// 关闭优先回复时，仍先完成本次已发现的消息批次，然后让出扫描机会。
	if w.ReGreet {
		return ReGreet
	}
	if w.Reply {
		return Reply
	}
	if w.Greeting {
		return Greeting
	}
	return Done
}

// BatchLimit 限制一个消息批次最多三人或六十秒，只在完整候选人结束后判断。
func BatchLimit(start, now time.Time, processed int) bool {
	return processed >= 3 || now.Sub(start) >= 60*time.Second
}

// AnchorsMatch 比较对应位置的完整有序标识；调用方须先确认岗位、筛选条件和相邻位置。
func AnchorsMatch(saved, current []string) bool {
	if len(saved) == 0 || len(saved) > 3 || len(saved) != len(current) {
		return false
	}
	for i, id := range saved {
		if id == "" || id != current[i] {
			return false
		}
	}
	return true
}
