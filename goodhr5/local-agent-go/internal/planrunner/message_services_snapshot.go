// 本文件保存 HRPlus 父计划消息服务原来源与公平性，恢复后只保留规范运行仍已激活的入口。
package planrunner

import (
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"reflect"
	"sort"
	"time"
)

// SavedMessageSource 保存原项原任务的等待事实，不保存页面或凭证。
type SavedMessageSource struct {
	ItemID, TaskID           string
	Order                    int
	Pending                  bool
	WaitingSince, LastServed time.Time
}

// SavedMessageService 保存同岗位动作入口及已发现来源，最新策略由规范运行重新决定。
type SavedMessageService struct {
	Key          MessageServiceKey
	Sources      []SavedMessageSource
	NextCheck    time.Time
	FinalChecked bool
}

// MessageServicesSnapshot 固定账号、原批次配置与序号，扫描预算不由时钟推算。
type MessageServicesSnapshot struct {
	Schema         int
	OwnerScope     string
	Run            planmodel.Run
	Services       []SavedMessageService
	PagesSinceScan int
	LastAction     string
	CycleServed    bool
}

// Snapshot 生成确定排序的安全快照，同序号重试不能因 map 遍历顺序产生不同内容。
func (s *MessageServices) Snapshot() ([]byte, error) {
	if s.lastRun == nil {
		return nil, fmt.Errorf("消息服务尚未核对原运行")
	}
	value := MessageServicesSnapshot{Schema: 1, OwnerScope: s.ownerScope, Run: *s.lastRun, PagesSinceScan: s.pagesSinceScan, LastAction: s.lastAction, CycleServed: s.cycleServed}
	for key, entry := range s.services {
		row := SavedMessageService{Key: key, NextCheck: entry.nextCheck, FinalChecked: entry.finalChecked}
		for _, source := range entry.sources {
			row.Sources = append(row.Sources, SavedMessageSource{source.itemID, source.taskID, source.order, source.pending, source.waitingSince, source.lastServed})
		}
		sort.Slice(row.Sources, func(i, j int) bool { return row.Sources[i].ItemID < row.Sources[j].ItemID })
		value.Services = append(value.Services, row)
	}
	sort.Slice(value.Services, func(i, j int) bool {
		a, b := value.Services[i].Key, value.Services[j].Key
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.PositionID != b.PositionID {
			return a.PositionID < b.PositionID
		}
		return a.Action < b.Action
	})
	return json.Marshal(value)
}

// Restore 不把快照当激活许可；原配置一致、当前规范序号不倒退后，再过滤已经结束的来源。
func (s *MessageServices) Restore(raw []byte, run planmodel.Run, platformForItem func(string) (string, error)) error {
	var value MessageServicesSnapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if value.Schema != 1 || value.OwnerScope != s.ownerScope || value.PagesSinceScan < 0 || (value.LastAction != "" && value.LastAction != "auto_reply" && value.LastAction != "re_greet") {
		return fmt.Errorf("原消息服务快照无效")
	}
	old := value.Run
	if err := old.Validate(); err != nil {
		return err
	}
	if old.ID != run.ID || old.PlanID != run.PlanID || old.ActivationID != run.ActivationID || old.ExecutionDate != run.ExecutionDate || old.ConfigVersion != run.ConfigVersion || old.Sequence > run.Sequence || !reflect.DeepEqual(old.Snapshot, run.Snapshot) {
		return fmt.Errorf("消息服务不属于原批次或日期")
	}
	frozen := NewMessageServices(s.ownerScope)
	if err := frozen.Sync(old, platformForItem); err != nil {
		return err
	}
	seen := map[MessageServiceKey]bool{}
	for _, row := range value.Services {
		entry := frozen.services[row.Key]
		if entry == nil || seen[row.Key] || len(row.Sources) != len(entry.sources) {
			return fmt.Errorf("原消息入口身份不匹配")
		}
		seen[row.Key] = true
		sources := map[string]bool{}
		for _, source := range row.Sources {
			original, exists := entry.sources[source.ItemID]
			if !exists || sources[source.ItemID] || original.taskID != source.TaskID || original.order != source.Order {
				return fmt.Errorf("原消息工作来源不匹配")
			}
			sources[source.ItemID] = true
			original.pending = source.Pending
			original.waitingSince = source.WaitingSince
			original.lastServed = source.LastServed
			entry.sources[source.ItemID] = original
		}
		entry.nextCheck = row.NextCheck
		entry.finalChecked = row.FinalChecked
	}
	if len(seen) != len(frozen.services) {
		return fmt.Errorf("原消息入口记录不完整")
	}
	frozen.pagesSinceScan = value.PagesSinceScan
	frozen.lastAction = value.LastAction
	frozen.cycleServed = value.CycleServed
	if err := frozen.Sync(run, platformForItem); err != nil {
		return err
	}
	*s = *frozen
	return nil
}
