// 本文件覆盖复打招呼相关的本地 Agent 逻辑：任务类型识别、个人配置抽取、随机间隔。
package positionrunner

import (
	"testing"
	"time"
)

// TestNormalizeTaskType_ReGreet 验证 normalizeTaskType 接受 re_greet。
func TestNormalizeTaskType_ReGreet(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "greeting"},
		{"greeting", "greeting"},
		{"auto_reply", "auto_reply"},
		{"re_greet", "re_greet"},
		{"greeting,auto_reply", "greeting"},
		{"auto_reply,re_greet", "auto_reply"},
		{"re_greet,greeting", "re_greet"},
	}
	for _, c := range cases {
		got, err := normalizeTaskType(c.in)
		if err != nil {
			t.Fatalf("normalizeTaskType(%q) 错误：%v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("normalizeTaskType(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	if _, err := normalizeTaskType("unknown"); err == nil {
		t.Fatalf("未知任务类型应报错")
	}
}

// TestParseTaskTypes_ReGreet 验证 parseTaskTypes 把 re_greet 视作有效类型并去重。
func TestParseTaskTypes_ReGreet(t *testing.T) {
	got := parseTaskTypes("greeting,auto_reply,re_greet")
	if len(got) != 3 || !hasTaskType(got, "re_greet") || !hasTaskType(got, "auto_reply") || !hasTaskType(got, "greeting") {
		t.Fatalf("三选期望全部命中，实际：%v", got)
	}
	dedup := parseTaskTypes("re_greet,re_greet,greeting")
	if len(dedup) != 2 {
		t.Fatalf("去重后应为 2，实际：%v", dedup)
	}
	filtered := parseTaskTypes("re_greet,unknown,greeting")
	if len(filtered) != 2 || hasTaskType(filtered, "unknown") {
		t.Fatalf("未知类型应被过滤：%v", filtered)
	}
}

// TestExtractReGreetPrefs_Defaults 验证启动参数全缺时回退到清单默认值。
func TestExtractReGreetPrefs_Defaults(t *testing.T) {
	prefs := extractReGreetPrefs(StartOptions{})
	if prefs.intervalMinMinutes != 30 || prefs.intervalMaxMinutes != 50 || prefs.timeRangeDays != 7 || prefs.maxCount != 1 {
		t.Fatalf("默认值不符合清单：%+v", prefs)
	}
}

// TestExtractReGreetPrefs_Override 验证启动参数覆盖默认值，并修正 max < min 的非法区间。
func TestExtractReGreetPrefs_Override(t *testing.T) {
	prefs := extractReGreetPrefs(StartOptions{
		ReGreetIntervalMin: 60,
		ReGreetIntervalMax: 40, // 小于 min，应被修正为 min
		ReGreetTimeRange:   14,
		ReGreetMaxCount:    3,
	})
	if prefs.intervalMinMinutes != 60 {
		t.Fatalf("intervalMinMinutes 应被覆盖为 60，实际 %d", prefs.intervalMinMinutes)
	}
	if prefs.intervalMaxMinutes != 60 {
		t.Fatalf("intervalMaxMinutes 小于 min 时应被修正为 min，实际 %d", prefs.intervalMaxMinutes)
	}
	if prefs.timeRangeDays != 14 || prefs.maxCount != 3 {
		t.Fatalf("time_range / max_count 应被覆盖：%+v", prefs)
	}
}

// TestRandomInterval_Bounds 验证随机间隔落在 [min, max] 区间内。
func TestRandomInterval_Bounds(t *testing.T) {
	minMinutes := 30
	maxMinutes := 50
	for i := 0; i < 200; i++ {
		d := randomInterval(minMinutes, maxMinutes)
		if d < time.Duration(minMinutes)*time.Minute || d > time.Duration(maxMinutes)*time.Minute {
			t.Fatalf("随机间隔越界：%v", d)
		}
	}
	if randomInterval(10, 10) != 10*time.Minute {
		t.Fatalf("min == max 时应返回固定值")
	}
	if randomInterval(20, 5) != 20*time.Minute {
		t.Fatalf("min > max 时应回退到 min")
	}
}
