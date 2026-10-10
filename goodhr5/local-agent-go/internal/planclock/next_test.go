// 本文件验证 HRPlus 下次名义时间的时区、远期生效、每周与一次性到期规则。
package planclock

import (
	"testing"
	"time"
)

// TestNextNominalStart 验证远期日期不受固定搜索天数截断，一次性完成后没有虚构下次执行。
func TestNextNominalStart(t *testing.T) {
	s := Schedule{Cycle: "daily", Timezone: "Asia/Shanghai", StartDate: "2030-01-01", Windows: []Window{{Order: 0, StartMinute: 540, EndMinute: 720}}}
	now := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	next, err := s.NextStart(now)
	if err != nil || next == nil || next.Format("2006-01-02 15:04") != "2030-01-01 09:00" {
		t.Fatal("远期生效日期错误", err, next)
	}
	s.Cycle = "once"
	s.OnceDate = "2030-01-01"
	if next, err := s.NextStart(time.Date(2030, 1, 1, 4, 0, 0, 0, time.UTC)); err != nil || next != nil {
		t.Fatal("一次性任务虚构下一次", err, next)
	}
	s.Cycle = "weekly"
	s.StartDate = ""
	s.OnceDate = ""
	s.Weekdays = []int{1}
	next, err = s.NextStart(now)
	if err != nil || next == nil || next.Format("2006-01-02") != "2026-10-12" {
		t.Fatal("每周执行日期错误", err, next)
	}
}
