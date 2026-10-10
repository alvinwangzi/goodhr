// 本文件编排 HRPlus 已激活消息服务，重复岗位动作共享入口，保留原工作归属和最新开始项的动作策略。
package planrunner

import (
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
	"sort"
	"time"
)

// MessageServiceKey 按真实所有者、平台、岗位和动作区分服务，不按候选人姓名或当前主项合并。
type MessageServiceKey struct {
	OwnerScope, Platform, PositionID, Action string
}

// MessageChoice 将原工作来源和最新动作设置分开，扫描与检查也由同一父调度选择。
type MessageChoice struct {
	Kind, ItemRunID, TaskRunID, PolicyItemRunID, Action string
	ForceCheck                                          bool
	Key                                                 MessageServiceKey
}

// messageSource 保存原项待处理事实；时间只影响公平顺序，不给予页面执行权。
type messageSource struct {
	itemID, taskID string
	order          int
	pending        bool
	waitingSince   time.Time
	lastServed     time.Time
}

// messageService 同一入口保留多个已发现工作来源，策略由最近开始项提供。
type messageService struct {
	key          MessageServiceKey
	policy       messageSource
	priority     bool
	sources      map[string]messageSource
	nextCheck    time.Time
	finalChecked bool
}

// MessageServices 保存父运行的活跃服务与扫描预算，不操作页面且不注册尚未开始的项。
type MessageServices struct {
	ownerScope     string
	runID          string
	services       map[MessageServiceKey]*messageService
	pagesSinceScan int
	lastAction     string
	identity       *planmodel.Run
	cycleServed    bool
	lastRun        *planmodel.Run
}

// NewMessageServices 创建当前账号作用域的消息编排，空作用域不能注册服务。
func NewMessageServices(ownerScope string) *MessageServices {
	return &MessageServices{ownerScope: ownerScope, services: map[MessageServiceKey]*messageService{}}
}

// Sync 从已保存运行重建活跃入口，保留仍活跃来源的等待顺序；平台必须来自对应已准备检查点。
func (s *MessageServices) Sync(run planmodel.Run, platformForItem func(string) (string, error)) error {
	if s.ownerScope == "" || platformForItem == nil || (s.runID != "" && s.runID != run.ID) {
		return fmt.Errorf("消息编排缺少原运行或所有者")
	}
	if err := run.Validate(); err != nil {
		return err
	}
	if s.lastRun != nil && (run.Sequence < s.lastRun.Sequence || (run.Sequence == s.lastRun.Sequence && !reflect.DeepEqual(run, *s.lastRun))) {
		return fmt.Errorf("消息编排收到旧序号或同序号不同进度")
	}
	if s.identity != nil && (s.identity.PlanID != run.PlanID || s.identity.ActivationID != run.ActivationID || s.identity.ExecutionDate != run.ExecutionDate || s.identity.ConfigVersion != run.ConfigVersion || !reflect.DeepEqual(s.identity.Snapshot, run.Snapshot)) {
		return fmt.Errorf("消息编排原批次或配置已变化")
	}
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	var frozen planmodel.Run
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return err
	}
	next := map[MessageServiceKey]*messageService{}
	platforms := map[string]string{}
	for _, item := range run.Items {
		for _, action := range []string{"auto_reply", "re_greet"} {
			if item.Actions[action].State != "active" {
				continue
			}
			if item.Order > run.CurrentItem || (item.State != "running" && item.State != "completed") || !planmodel.ValidID(item.TaskRunID) {
				return fmt.Errorf("未准备或未开始的消息项不能激活")
			}
			platform := platforms[item.ID]
			if platform == "" {
				platform, err = platformForItem(item.ID)
				if err != nil {
					return err
				}
				platforms[item.ID] = platform
			}
			if platform == "" {
				return fmt.Errorf("消息项缺少原平台")
			}
			key := MessageServiceKey{s.ownerScope, platform, item.Snapshot.PositionID, action}
			entry := next[key]
			if entry == nil {
				entry = &messageService{key: key, sources: map[string]messageSource{}}
				if old := s.services[key]; old != nil {
					entry.nextCheck, entry.finalChecked = old.nextCheck, old.finalChecked
				}
				next[key] = entry
			}
			source := messageSource{itemID: item.ID, taskID: item.TaskRunID, order: item.Order}
			if old := s.services[key]; old != nil {
				if prior, ok := old.sources[item.ID]; ok {
					source.pending, source.waitingSince, source.lastServed = prior.pending, prior.waitingSince, prior.lastServed
				}
			}
			entry.sources[item.ID] = source
			if entry.policy.itemID == "" || source.order > entry.policy.order {
				entry.policy, entry.priority = source, item.Snapshot.PrioritizeReply
			}
		}
	}
	for key, entry := range next {
		if old := s.services[key]; old != nil && old.policy.itemID != entry.policy.itemID {
			entry.nextCheck = time.Time{}
			entry.finalChecked = false
		}
	}
	s.runID, s.services = run.ID, next
	if s.identity == nil {
		s.identity = &frozen
	}
	s.lastRun = &frozen
	return nil
}

// Next 在安全边界选择一个消息入口或扫描，优先回复不让到期复打和扫描长期等待。
func (s *MessageServices) Next(now time.Time, greetingRemaining, prioritizeReply, finalPass bool) MessageChoice {
	checkDue, priorityEnabled := false, prioritizeReply
	for _, entry := range s.services {
		checkDue = checkDue || entry.nextCheck.IsZero() || !now.Before(entry.nextCheck)
		priorityEnabled = priorityEnabled || (entry.key.Action == "auto_reply" && entry.priority)
	}
	if greetingRemaining && s.cycleServed && !priorityEnabled && !checkDue {
		return MessageChoice{Kind: "scan"}
	}
	if checkDue {
		s.cycleServed = false
	}
	if greetingRemaining && s.pagesSinceScan >= 2 {
		return MessageChoice{Kind: "scan"}
	}
	choices := []MessageChoice{}
	for key, entry := range s.services {
		pending := []messageSource{}
		for _, source := range entry.sources {
			if source.pending {
				pending = append(pending, source)
			}
		}
		sort.Slice(pending, func(i, j int) bool { return messageSourceBefore(pending[i], pending[j]) })
		if len(pending) > 0 {
			source := pending[0]
			choices = append(choices, MessageChoice{Kind: "message", ItemRunID: source.itemID, TaskRunID: source.taskID, PolicyItemRunID: entry.policy.itemID, Action: key.Action, Key: key, ForceCheck: entry.nextCheck.IsZero() || !now.Before(entry.nextCheck)})
		} else if (finalPass && !entry.finalChecked) || (!finalPass && (entry.nextCheck.IsZero() || !now.Before(entry.nextCheck))) {
			choices = append(choices, MessageChoice{Kind: "message", ItemRunID: entry.policy.itemID, TaskRunID: entry.policy.taskID, PolicyItemRunID: entry.policy.itemID, Action: key.Action, ForceCheck: true, Key: key})
		}
	}
	hasReply := false
	for _, choice := range choices {
		hasReply = hasReply || choice.Action == "auto_reply"
	}
	allowUrgentReGreet := s.lastAction != "re_greet" || !hasReply
	sort.Slice(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		aSource, bSource := s.services[a.Key].sources[a.ItemRunID], s.services[b.Key].sources[b.ItemRunID]
		aOverdue := allowUrgentReGreet && a.Action == "re_greet" && aSource.pending && !now.Before(aSource.waitingSince.Add(time.Minute))
		bOverdue := allowUrgentReGreet && b.Action == "re_greet" && bSource.pending && !now.Before(bSource.waitingSince.Add(time.Minute))
		if aOverdue != bOverdue {
			return aOverdue
		}
		if aSource.lastServed.IsZero() != bSource.lastServed.IsZero() {
			return aSource.lastServed.IsZero() // 尚未检查的复打入口不能始终排在持续回复后面。
		}
		aPriority := a.Action == "auto_reply" && (prioritizeReply || s.services[a.Key].priority)
		bPriority := b.Action == "auto_reply" && (prioritizeReply || s.services[b.Key].priority)
		if aPriority != bPriority {
			return aPriority
		}
		if aSource.waitingSince.Equal(bSource.waitingSince) && aSource.lastServed.Equal(bSource.lastServed) && aSource.order == bSource.order {
			return a.Action < b.Action
		}
		return messageSourceBefore(aSource, bSource)
	})
	if len(choices) > 0 {
		return choices[0]
	}
	if greetingRemaining {
		return MessageChoice{Kind: "scan"}
	}
	return MessageChoice{Kind: "done"} // 无当前工作不等待下一次周期检查或未来复打。
}

// messageSourceBefore 按首次等待、上次处理和原编排顺序分配消息机会。
func messageSourceBefore(a, b messageSource) bool {
	// 尚未获得机会的入口先处理；持续有新消息的旧入口不能始终占据最早等待位置。
	if !a.lastServed.Equal(b.lastServed) {
		return a.lastServed.Before(b.lastServed)
	}
	if !a.waitingSince.Equal(b.waitingSince) {
		if a.waitingSince.IsZero() {
			return false
		}
		if b.waitingSince.IsZero() {
			return true
		}
		return a.waitingSince.Before(b.waitingSince)
	}
	return a.order < b.order
}

// Observed 保存该批次原来源的剩余工作，检查与消息批次都消耗一次页面预算；未知来源拒绝写入。
func (s *MessageServices) Observed(choice MessageChoice, remaining bool, now time.Time, finalPass bool) error {
	entry := s.services[choice.Key]
	if choice.Kind != "message" || entry == nil || choice.PolicyItemRunID != entry.policy.itemID {
		return fmt.Errorf("消息结果与当前服务设置不匹配")
	}
	source, exists := entry.sources[choice.ItemRunID]
	if !exists || choice.TaskRunID != source.taskID || choice.Action != choice.Key.Action {
		return fmt.Errorf("消息结果原归属不匹配")
	}
	if remaining && !source.pending {
		source.waitingSince = now
	}
	if !remaining {
		source.waitingSince = time.Time{}
	}
	source.pending, source.lastServed = remaining, now
	entry.sources[choice.ItemRunID] = source
	if choice.ForceCheck || entry.nextCheck.IsZero() {
		entry.nextCheck = now.Add(time.Minute)
	}
	entry.finalChecked = finalPass && !remaining
	s.pagesSinceScan++
	s.lastAction = choice.Action
	return nil
}

// Scanned 在一个实际扫描安全单元结束后重置消息页面预算，不根据时间猜测已处理候选人。
func (s *MessageServices) Scanned() {
	if s.pagesSinceScan > 0 {
		s.cycleServed = true
	}
	s.pagesSinceScan = 0
}
