// 本文件定义 HRPlus 执行计划的本地日期与名义时间段规则，不执行页面动作或持有登录凭证。
package planclock

import (
	"fmt"
	"sort"
	"time"
	_ "time/tzdata"
)

// Window 表示同一天内的名义时间段，分钟值不包含随机收尾延后。
type Window struct {
	Order       int `json:"order"`
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// Schedule 保存周期、有效日期、时区及多个名义时间段；星期按周一一至周日七编码。
type Schedule struct {
	Cycle     string   `json:"cycle"`
	Timezone  string   `json:"timezone"`
	OnceDate  string   `json:"once_date,omitempty"`
	StartDate string   `json:"start_date,omitempty"`
	EndDate   string   `json:"end_date,omitempty"`
	Weekdays  []int    `json:"weekdays,omitempty"`
	Windows   []Window `json:"windows"`
}

// Interval 保存真实时区中的窗口边界及本地执行日期，用于持久化运行与收尾时间。
type Interval struct {
	Date       string
	Order      int
	Start, End time.Time
}

// Validate 拒绝未知周期、错误日期和重叠时间段；相邻窗口不要求额外六分钟间隔。
func (s Schedule) Validate() error {
	if s.Cycle != "once" && s.Cycle != "daily" && s.Cycle != "weekly" {
		return fmt.Errorf("请选择一次性、每天或每周周期")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil || s.Timezone == "" || s.Timezone == "Local" {
		return fmt.Errorf("计划时区无法识别")
	}
	for _, date := range []string{s.OnceDate, s.StartDate, s.EndDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("计划日期必须是有效的年月日")
			}
		}
	}
	if s.Cycle == "once" && s.OnceDate == "" {
		return fmt.Errorf("一次性计划需要执行日期")
	}
	if s.StartDate != "" && s.EndDate != "" && s.StartDate > s.EndDate {
		return fmt.Errorf("有效结束日期不能早于开始日期")
	}
	if s.Cycle == "once" && ((s.StartDate != "" && s.OnceDate < s.StartDate) || (s.EndDate != "" && s.OnceDate > s.EndDate)) {
		return fmt.Errorf("一次性执行日期不在有效期内")
	}
	seen := map[int]bool{}
	for _, day := range s.Weekdays {
		if day < 1 || day > 7 || seen[day] {
			return fmt.Errorf("执行星期必须是一至七且不重复")
		}
		seen[day] = true
	}
	if s.Cycle == "weekly" && len(seen) == 0 {
		return fmt.Errorf("每周计划至少选择一天")
	}
	if len(s.Windows) == 0 {
		return fmt.Errorf("计划至少需要一个时间段")
	}
	ordered := append([]Window{}, s.Windows...)
	for index, w := range s.Windows {
		if w.Order != index {
			return fmt.Errorf("时间段顺序必须连续")
		}
		if w.StartMinute < 0 || w.EndMinute > 1440 || w.StartMinute >= w.EndMinute {
			return fmt.Errorf("时间段必须在一天内且开始早于结束")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StartMinute < ordered[j].StartMinute })
	for i := 1; i < len(ordered); i++ {
		if ordered[i].StartMinute < ordered[i-1].EndMinute {
			return fmt.Errorf("同一计划的时间段不能重叠")
		}
	}
	return nil
}

// dateAllowed 依据本地日历判断是否执行，不依赖电脑或服务器的默认时区。
func (s Schedule) dateAllowed(day time.Time) bool {
	date := day.Format("2006-01-02")
	if (s.StartDate != "" && date < s.StartDate) || (s.EndDate != "" && date > s.EndDate) {
		return false
	}
	if s.Cycle == "once" {
		return date == s.OnceDate
	}
	if s.Cycle == "weekly" {
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		for _, w := range s.Weekdays {
			if w == weekday {
				return true
			}
		}
		return false
	}
	return true
}

// OnDate 返回给定时刻所属本地执行日的窗口，始终按实际开始时间排序。
func (s Schedule) OnDate(now time.Time) ([]Interval, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	loc, _ := time.LoadLocation(s.Timezone)
	day := now.In(loc)
	if !s.dateAllowed(day) {
		return nil, nil
	}
	result := []Interval{}
	for _, w := range s.Windows {
		start := time.Date(day.Year(), day.Month(), day.Day(), w.StartMinute/60, w.StartMinute%60, 0, 0, loc)
		end := time.Date(day.Year(), day.Month(), day.Day(), w.EndMinute/60, w.EndMinute%60, 0, 0, loc)
		if !end.After(start) {
			return nil, fmt.Errorf("当前日期的时间段无法建立，请调整时区或时间")
		}
		result = append(result, Interval{Date: day.Format("2006-01-02"), Order: w.Order, Start: start, End: end})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	return result, nil
}

// Current 只允许在名义时段内新建或领取执行，结束点和随机延后区间不能启动新任务。
func (s Schedule) Current(now time.Time) (*Interval, error) {
	intervals, err := s.OnDate(now)
	if err != nil {
		return nil, err
	}
	for _, w := range intervals {
		if !now.Before(w.Start) && now.Before(w.End) {
			copy := w
			return &copy, nil
		}
	}
	return nil, nil
}

// FinishAt 校验已抽样的收尾延迟；抽样值由持久化模块保存，本方法不重新随机。
func (w Interval) FinishAt(grace time.Duration) (time.Time, error) {
	if grace < 3*time.Minute || grace > 6*time.Minute {
		return time.Time{}, fmt.Errorf("随机收尾延迟必须为三至六分钟")
	}
	return w.End.Add(grace), nil
}
