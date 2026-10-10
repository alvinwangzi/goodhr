// 本文件用真实 SQLite 验证 HRPlus 父消息服务快照版本门槛、账号隔离和重开恢复。
package localdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// TestPlanMessageDispatchReopen 验证快照只关联当前规范运行，旧序号不能写回后来轮换。
func TestPlanMessageDispatchReopen(t *testing.T) {
	db, cfg := openReplyDB(t)
	_, run := snapshotFixture(t)
	if err := db.SavePlanRunSnapshot(t.Context(), "owner", run); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "owner", "Run": run})
	if err := db.SavePlanMessageDispatch(t.Context(), "owner", run.ID, run.Sequence, raw); err != nil {
		t.Fatal(err)
	}
	future := run
	future.Sequence++
	futureRaw, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "owner", "Run": future})
	if err := db.SavePlanMessageDispatch(t.Context(), "owner", run.ID, run.Sequence+1, futureRaw); !errors.Is(err, ErrPlanSnapshotStale) {
		t.Fatal("未确认序号可以保存", err)
	}
	if _, err := db.PlanMessageDispatch(t.Context(), "other", run.ID); err == nil {
		t.Fatal("其他账号能读原轮换")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.PlanMessageDispatch(t.Context(), "owner", run.ID)
	if err != nil || !bytes.Equal(actual, raw) {
		t.Fatal("重开后原轮换丢失", err)
	}
}
