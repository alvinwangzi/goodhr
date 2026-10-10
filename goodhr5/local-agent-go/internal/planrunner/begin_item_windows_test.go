// 本文件验证 HRPlus 从原云端准备到 M1 检查点和子引用的受控启动链，不运行招聘页面。
package planrunner

import (
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"goodhr5/local-agent-go/internal/positionrunner"
	"strings"
	"sync/atomic"
	"testing"
)

// TestBeginItemM1Binding 验证协调器沿用原任务、恢复本项计数、不释放父预留和不输出登录配置。
func TestBeginItemM1Binding(t *testing.T) {
	mode := &atomic.Int32{}
	c, plan, claim, authority, _, _ := acquireFixture(t, mode, func(p *planmodel.Permit) {
		p.Run.Snapshot.Items[0].Actions = []string{"greeting"}
		p.Run.Snapshot.Items[0].PrioritizeReply = false
		p.Run.Items[0].Snapshot = p.Run.Snapshot.Items[0]
		p.Run.Items[0].Actions = map[string]planmodel.ActionProgress{"greeting": {State: "pending"}}
	})
	held, err := c.Acquire(t.Context(), plan, claim, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Reservation.Release(true)
	requestID := "60000000-0000-0000-0000-000000000004"
	prepared, err := c.BeginItem(t.Context(), held, held.Permit.Run, requestID, authority, positionrunner.StartOptions{TaskType: "auto_reply", Token: "ignored-token"})
	if err != nil || prepared.Reservation == nil || prepared.Snapshot.Options.TaskType != "greeting" || prepared.Snapshot.Options.Token != authority.Token || prepared.Snapshot.Options.CloudRunID != prepared.Permit.Run.Items[0].TaskRunID {
		t.Fatal("M1 准备未继承原许可", err)
	}
	checkpoint, err := c.db.LoadActionCheckpoint(t.Context(), prepared.Snapshot.Options.LocalRunID)
	if err != nil || checkpoint.PlanRunID != claim.RunID || checkpoint.CloudRunID != prepared.Permit.Run.Items[0].TaskRunID {
		t.Fatal("M1 检查点原归属缺失", err)
	}
	checkpoint.Greeted = 1
	if err = c.db.SaveActionCheckpoint(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Reservation.ReleaseAfterCleanup(true); err != nil || !held.Reservation.Valid() {
		t.Fatal("主项交还引用清除了父占用", err)
	}
	restored, err := c.BeginItem(t.Context(), held, held.Permit.Run, requestID, authority, positionrunner.StartOptions{})
	if err != nil || restored.Snapshot.Options.LocalRunID != checkpoint.RunID || restored.Permit.Run.Items[0].TaskRunID != checkpoint.CloudRunID {
		t.Fatal("再次准备创建新任务或检查点", err)
	}
	defer restored.Reservation.ReleaseAfterCleanup(true)
	loaded, err := c.db.LoadActionCheckpoint(t.Context(), checkpoint.RunID)
	if err != nil || loaded.Greeted != 1 {
		t.Fatal("再次准备重置数量", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, restored), authority.Token) {
			t.Fatal("准备结果日志输出令牌")
		}
	}
	raw, err := json.Marshal(restored)
	if err != nil || strings.Contains(string(raw), authority.Token) {
		t.Fatal("准备结果序列化输出令牌", err)
	}
}
