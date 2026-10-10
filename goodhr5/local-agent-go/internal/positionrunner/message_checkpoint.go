// 本文件按原执行项持久化 HRPlus 消息工作名单，恢复名单仍走现有页面核对和发送防重流程。
package positionrunner

import (
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/actiondispatch"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"time"
)

// savedDueReGreet 只保存候选人事实、原到期和基准，不保存运行句柄。
type savedDueReGreet struct {
	Candidate cloudapi.ReGreetCandidate `json:"candidate"`
	Due       time.Time                 `json:"due"`
	Basis     string                    `json:"basis"`
	QueuedAt  time.Time                 `json:"queued_at"`
}

// savedMessageState 把消息来源绑定到原任务，不因最新岗位策略改写归属。
type savedMessageState struct {
	Schema                                                                                 int `json:"schema"`
	RunID, PlanRunID, ItemRunID, TaskRunID, OwnerScope, PositionID, Platform, ProfileScope string
	Replies                                                                                []platformcore.ReplyConversation `json:"replies"`
	ReGreets                                                                               []savedDueReGreet                `json:"re_greets"`
	HandledReGreets                                                                        map[string]bool                  `json:"handled_re_greets"`
	InfoQueue                                                                              []string                         `json:"info_queue"`
	HandledInfo                                                                            map[string]bool                  `json:"handled_info"`
	LastChecked                                                                            time.Time                        `json:"last_checked"`
	Scheduler                                                                              actiondispatch.Snapshot          `json:"scheduler"`
}

// messageState 保存安全字段；计数与队列由同一个检查点写入，不能只恢复累计值。
func (s *actionSession) messageState(cp localdb.ActionCheckpoint) (json.RawMessage, error) {
	value := savedMessageState{Schema: 1, RunID: cp.RunID, PlanRunID: cp.PlanRunID, ItemRunID: cp.ItemRunID, TaskRunID: cp.CloudRunID, OwnerScope: cp.OwnerScope, PositionID: cp.PositionID, Platform: cp.Platform, ProfileScope: cp.ProfileScope, Replies: s.replies, HandledReGreets: s.handledReGreets, InfoQueue: s.infoQueue, HandledInfo: s.handledInfo, LastChecked: s.lastChecked, Scheduler: s.scheduler.Snapshot()}
	for _, item := range s.reGreets {
		value.ReGreets = append(value.ReGreets, savedDueReGreet{Candidate: item.candidate, Due: item.due, Basis: item.basis, QueuedAt: item.queuedAt})
	}
	return json.Marshal(value)
}

// restoreMessageState 只恢复指定原检查点，DOM 锚点和页面定位必须重新取得。
func (s *actionSession) restoreMessageState(cp localdb.ActionCheckpoint) error {
	if len(cp.MessageState) == 0 {
		return nil
	}
	var value savedMessageState
	if err := json.Unmarshal(cp.MessageState, &value); err != nil {
		return err
	}
	if value.Schema != 1 || value.RunID != cp.RunID || value.PlanRunID != cp.PlanRunID || value.ItemRunID != cp.ItemRunID || value.TaskRunID != cp.CloudRunID || value.OwnerScope != cp.OwnerScope || value.PositionID != cp.PositionID || value.Platform != cp.Platform || value.ProfileScope != cp.ProfileScope {
		return fmt.Errorf("消息名单不属于原执行项或账号")
	}
	for _, conversation := range value.Replies {
		if conversation.ID == "" {
			return fmt.Errorf("原消息名单缺少真实会话 ID")
		}
	}
	for _, item := range value.ReGreets {
		if item.Candidate.PlatformCandidateID == "" || item.Basis == "" || item.Due.IsZero() || item.Candidate.PositionID != cp.PositionID {
			return fmt.Errorf("原复打名单身份不完整")
		}
	}
	if err := s.scheduler.Restore(value.Scheduler); err != nil {
		return err
	}
	s.replies = value.Replies
	s.reGreets = nil
	for _, item := range value.ReGreets {
		s.reGreets = append(s.reGreets, dueReGreet{candidate: item.Candidate, due: item.Due, basis: item.Basis, queuedAt: item.QueuedAt})
	}
	s.handledReGreets = value.HandledReGreets
	if s.handledReGreets == nil {
		s.handledReGreets = map[string]bool{}
	}
	s.infoQueue = value.InfoQueue
	s.handledInfo = value.HandledInfo
	s.lastChecked = value.LastChecked
	return nil
}
