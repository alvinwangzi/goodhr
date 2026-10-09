// 本文件验证 HRPlus 计划日历、窗口边界与收尾语义，不使用真实时钟等待或招聘页面。
package planclock

import (
	"testing"
	"time"
)

// testSchedule 创建与已确认上午下午编排一致的虚构配置。
func testSchedule() Schedule {
	return Schedule{Cycle: "daily", Timezone: "Asia/Shanghai", Windows: []Window{{Order: 0, StartMinute: 540, EndMinute: 720}, {Order: 1, StartMinute: 810, EndMinute: 1200}}}
}

// TestNominalWindowBoundaries 验证开始包含、结束不包含，并且延后收尾不是新任务启动许可。
func TestNominalWindowBoundaries(t *testing.T) {
	s := testSchedule()
	for _, tc := range []struct {
		clock  string
		active bool
	}{{"08:59:59", false}, {"09:00:00", true}, {"11:59:59", true}, {"12:00:00", false}, {"12:04:00", false}, {"13:30:00", true}, {"20:00:00", false}} {
		now, _ := time.Parse(time.RFC3339, "2026-10-09T"+tc.clock+"+08:00")
		window, err := s.Current(now.UTC())
		if err != nil || (window != nil) != tc.active {
			t.Fatalf("%s %v %v", tc.clock, window, err)
		}
	}
	day, _ := time.Parse(time.RFC3339, "2026-10-09T09:00:00+08:00")
	intervals, _ := s.OnDate(day)
	end, err := intervals[0].FinishAt(4 * time.Minute)
	if err != nil || end.Format("15:04") != "12:04" {
		t.Fatal("收尾时间错误")
	}
	if _, err := intervals[0].FinishAt(2 * time.Minute); err == nil {
		t.Fatal("错误随机收尾被接受")
	}
}

// TestCalendarCycleAndValidity 验证一次性、周末、有效期和下一日期重新判定。
func TestCalendarCycleAndValidity(t *testing.T) {
	s := testSchedule()
	s.Cycle = "weekly"
	s.Weekdays = []int{1, 2, 3, 4, 5}
	s.StartDate = "2026-10-09"
	s.EndDate = "2026-10-12"
	for _, tc := range []struct {
		date   string
		active bool
	}{{"2026-10-08", false}, {"2026-10-09", true}, {"2026-10-10", false}, {"2026-10-12", true}, {"2026-10-13", false}} {
		now, _ := time.Parse(time.RFC3339, tc.date+"T09:00:00+08:00")
		w, err := s.Current(now)
		if err != nil || (w != nil) != tc.active {
			t.Fatalf("日期 %s %v", tc.date, err)
		}
	}
	s.Cycle = "once"
	s.OnceDate = "2026-10-09"
	now, _ := time.Parse(time.RFC3339, "2026-10-10T09:00:00+08:00")
	w, _ := s.Current(now)
	if w != nil {
		t.Fatal("一次性计划次日再次执行")
	}
}

// TestScheduleRejectsInvalidWindows 验证重叠、跨午夜、序号断裂与相邻窗口。
func TestScheduleRejectsInvalidWindows(t *testing.T) {
	for _, windows := range [][]Window{{{0, 600, 700}, {1, 650, 800}}, {{0, 1200, 60}}, {{1, 540, 720}}, {{0, -1, 60}}} {
		s := testSchedule()
		s.Windows = windows
		if s.Validate() == nil {
			t.Fatalf("无效时间段被接受 %v", windows)
		}
	}
	s := testSchedule()
	s.Windows = []Window{{0, 540, 720}, {1, 720, 1200}}
	if err := s.Validate(); err != nil {
		t.Fatal("相邻名义时段应允许", err)
	}
}
