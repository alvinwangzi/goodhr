// 本文件验证 HRPlus 本地计划契约拒绝缺失项、错配身份和未知动作，不操作招聘页面。
package planmodel

import (
	"encoding/json"
	"os"
	"testing"
)

// modelFixture 读取与云端结构一致的完整安全许可夹具。
func modelFixture(t *testing.T) Permit {
	t.Helper()
	raw, err := os.ReadFile("testdata/permit.json")
	if err != nil {
		t.Fatal(err)
	}
	var p Permit
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunContract 验证重复岗位独立身份、完整动作进度及不可变快照边界。
func TestRunContract(t *testing.T) {
	p := modelFixture(t)
	if err := p.Run.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Run){func(r *Run) { r.Items = r.Items[:1] }, func(r *Run) { r.Items[1].ID = r.Items[0].ID }, func(r *Run) { r.Items[0].Snapshot.PositionID = "foreign" }, func(r *Run) { r.Items[0].Actions["auto_reply"] = ActionProgress{State: "active", Count: -1} }, func(r *Run) { r.Items[1].Actions["unknown"] = ActionProgress{State: "active"} }, func(r *Run) { r.Snapshot.Schedule.Timezone = "Local" }} {
		fresh := modelFixture(t).Run
		change(&fresh)
		if fresh.Validate() == nil {
			t.Fatal("错误快照被接受")
		}
	}
}
