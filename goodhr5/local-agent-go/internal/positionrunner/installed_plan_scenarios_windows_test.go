// 本文件扩展 HRPlus 已安装程序的计划编排组合，全部动作使用独立数据和虚构招聘页面。
package positionrunner

import (
	"fmt"
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

// configureInstalledPlanScenario 为受控组合设置原计划，不改变客户端执行逻辑。
func configureInstalledPlanScenario(t *testing.T, config *planmodel.Config) {
	t.Helper()
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
