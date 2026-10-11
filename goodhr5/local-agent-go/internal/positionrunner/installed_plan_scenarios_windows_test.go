// 本文件扩展 HRPlus 已安装程序的计划编排组合，全部动作使用独立数据和虚构招聘页面。
package positionrunner

import (
	"fmt"
	"goodhr5/local-agent-go/internal/planclock"
	"goodhr5/local-agent-go/internal/planmodel"
	"os"
	"testing"
	"time"
)

// TestInstalledPlanFourItems 验证实装后台按四项顺序执行，重复岗位具有独立任务和检查点。
func TestInstalledPlanFourItems(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "four-items")
	runNativeReceiptRestart(t, true, true, true)
}

// TestInstalledPlanOutsideWindow 验证实装时段外立即开始只返回等待，不领取或打开招聘页面。
func TestInstalledPlanOutsideWindow(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "outside-window")
	runNativeReceiptRestart(t, true, true, true)
}

// TestInstalledPlanCompound 验证实装同岗位三动作穿插，各自数量和原任务归属保留。
func TestInstalledPlanCompound(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "compound")
	runNativeReceiptRestart(t, true, true, true)
}

// TestInstalledPlanTimedCompound 验证新回复和到期复打在扫描开始后出现，实际安装程序在扫描完成前穿插处理。
func TestInstalledPlanTimedCompound(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "timed-compound")
	runNativeReceiptRestart(t, true, true, true)
}

// TestInstalledExpiredLoginRestart 验证安装程序重启后拒绝已过期的原登录，不领取计划或补传数据。
func TestInstalledExpiredLoginRestart(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "expired-login")
	runNativeReceiptRestart(t, true)
}

// TestInstalledUnsupportedPlatform 验证安装程序拒绝未知平台和不支持消息动作的平台，且没有招聘页面动作。
func TestInstalledUnsupportedPlatform(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	for _, scenario := range []string{"unknown-platform", "unsupported-messages"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", scenario)
			runNativeReceiptRestart(t, true, true, true)
		})
	}
}

// TestInstalledNextDayOfflineReport 验证虚构的昨日未完成记录和离线日报不阻止安装程序从今日第一项启动。
func TestInstalledNextDayOfflineReport(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "next-day-offline-report")
	runNativeReceiptRestart(t, true, true, true)
}

// TestInstalledUnknownCandidateProtection 验证原发送结果未知的候选人在安装程序重启执行和报告重启时均不重发。
func TestInstalledUnknownCandidateProtection(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "unknown-send")
	runNativeReceiptRestart(t, true, true, true)
}

// nativeCompoundScenario 判断受控组合是否需要同一岗位的三类动作。
func nativeCompoundScenario() bool {
	scenario := os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO")
	return scenario == "compound" || scenario == "timed-compound" || scenario == "unknown-send"
}

// configureInstalledPlanScenario 为受控组合设置原计划，不改变客户端执行逻辑。

// TestInstalledPlanStopEditRestart 验证实装停止旧批次后交换岗位并从新第一项开始，原历史保留。
func TestInstalledPlanStopEditRestart(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "stop-edit")
	runNativeReceiptRestart(t, true, true, true)
}

// configureInstalledPlanScenario 为受控组合设置原计划，不改变客户端执行逻辑。

// TestInstalledPlanAcrossWindows 使用真实分钟边界及模拟休息，验证随机收尾后沿原运行续跑。
func TestInstalledPlanAcrossWindows(t *testing.T) {
	if os.Getenv("HRPLUS_M2_INSTALLED_AGENT_EXE") == "" {
		t.Skip("需明确已安装程序及摘要")
	}
	t.Setenv("HRPLUS_M2_ACCEPTANCE_SCENARIO", "across-windows")
	runNativeReceiptRestart(t, true, true, true)
}

// configureInstalledPlanScenario 为受控组合设置原计划，不改变客户端执行逻辑。
func configureInstalledPlanScenario(t *testing.T, config *planmodel.Config) {
	t.Helper()
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "unsupported-messages" {
		config.Items[0].Actions = []string{"auto_reply"}
		return
	}
	if nativeCompoundScenario() {
		config.Items[0].Actions = []string{"greeting", "auto_reply", "re_greet"}
		config.Items[0].PrioritizeReply = true
		return
	}
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "stop-edit" || os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "across-windows" {
		first := config.Items[0]
		second := first
		second.ID = "restart-sales"
		second.Order = 1
		second.PositionID = "native-sales"
		config.Items = append(config.Items, second)
		if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "across-windows" {
			config.Schedule.Timezone = "UTC"
			now := time.Now().UTC()
			minute := now.Hour()*60 + now.Minute()
			if minute > 1420 {
				t.Fatal("真实跨窗口验收需在UTC日末20分钟前启动")
			}
			config.Schedule.Windows = []planclock.Window{{Order: 0, StartMinute: minute, EndMinute: minute + 2}, {Order: 1, StartMinute: minute + 9, EndMinute: minute + 12}}
		}
		return
	}
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "outside-window" {
		zone, err := time.LoadLocation(config.Schedule.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().In(zone)
		start := (now.Hour()*60 + now.Minute() + 5) % 1440
		config.Schedule.Windows[0].StartMinute = start
		config.Schedule.Windows[0].EndMinute = start + 2
		if config.Schedule.Windows[0].EndMinute > 1440 {
			config.Schedule.Windows[0].EndMinute = 1440
		}
		return
	}
	if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") != "four-items" {
		return
	}
	template := config.Items[0]
	config.Items = nil
	for index := 0; index < 4; index++ {
		item := template
		item.ID = fmt.Sprintf("installed-item-%d", index+1)
		item.Order = index
		item.PositionID = "native-java"
		if index%2 == 1 {
			item.PositionID = "native-sales"
		}
		item.Actions = []string{"greeting"}
		item.PrioritizeReply = false
		if index >= 2 {
			item.Actions = []string{"auto_reply"}
			item.PrioritizeReply = true
		}
		config.Items = append(config.Items, item)
	}
}
