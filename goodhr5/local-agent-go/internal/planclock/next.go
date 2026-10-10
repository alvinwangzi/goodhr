// 本文件计算 HRPlus 执行计划的下个名义开始点，不给予执行权或承诺资源可用。
package planclock

import "time"

// NextStart 返回 now 之后的下个名义开始点，复用原时区、周期与有效日期规则。
func (s Schedule) NextStart(now time.Time) (*time.Time, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	loc, _ := time.LoadLocation(s.Timezone)
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	first := s.StartDate
	if s.Cycle == "once" {
		first = s.OnceDate
	}
	if first != "" && first > day.Format("2006-01-02") {
		day, _ = time.ParseInLocation("2006-01-02", first, loc)
	}
	for i := 0; i < 8; i++ {
		date := day.AddDate(0, 0, i)
		if s.EndDate != "" && date.Format("2006-01-02") > s.EndDate {
			return nil, nil
		}
		windows, err := s.OnDate(date)
		if err != nil {
			return nil, err
		}
		for _, window := range windows {
			if window.Start.After(now) {
				start := window.Start
				return &start, nil
			}
		}
	}
	return nil, nil
}
