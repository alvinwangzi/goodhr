// 本文件验证 HRPlus M2 编排结构，允许重复岗位但不接受重复执行项编号或含糊动作。
package httpapi

import "testing"

// validPlanConfig 创建同岗位先回复再找简历的已确认编排。
func validPlanConfig() ExecutionPlanConfig {
	return ExecutionPlanConfig{Name: "招聘计划", Schedule: ExecutionPlanSchedule{Cycle: "daily", Timezone: "Asia/Shanghai", Windows: []ExecutionPlanWindow{{0, 540, 720}, {1, 810, 1200}}}, Items: []ExecutionPlanItem{{ID: "first", PositionID: "same-job", Order: 0, Actions: []string{"auto_reply"}, PrioritizeReply: true}, {ID: "second", PositionID: "same-job", Order: 1, Actions: []string{"greeting"}}}}
}

// TestPlanConfigAllowsRepeatedPositions 验证重复岗位和相邻时段均有效，不替用户合并编排。
func TestPlanConfigAllowsRepeatedPositions(t *testing.T) {
	c := validPlanConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Schedule.Windows[1].StartMinute = 720
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestPlanConfigRejectsAmbiguity 验证未知动作、错序、重复编号、重叠及优先回复条件。
func TestPlanConfigRejectsAmbiguity(t *testing.T) {
	for _, change := range []func(*ExecutionPlanConfig){func(c *ExecutionPlanConfig) { c.Items[1].ID = "first" }, func(c *ExecutionPlanConfig) { c.Items[1].Order = 3 }, func(c *ExecutionPlanConfig) { c.Items[0].Actions = []string{"unknown"} }, func(c *ExecutionPlanConfig) { c.Items[1].PrioritizeReply = true }, func(c *ExecutionPlanConfig) { c.Schedule.Windows[1].StartMinute = 700 }, func(c *ExecutionPlanConfig) { c.Schedule.OnceDate = "2026-02-30" }} {
		c := validPlanConfig()
		change(&c)
		if c.Validate() == nil {
			t.Fatal("错误编排被接受")
		}
	}
}
