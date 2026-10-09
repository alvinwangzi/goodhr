// 本文件验证 HRPlus 原请求表升级保留历史顺序、密文和确认状态，准备请求不进入后台状态补传。
package localdb

import (
	"database/sql"
	"errors"
	"goodhr5/local-agent-go/internal/config"
	"reflect"
	"strings"
	"testing"
)

// TestPlanOperationsUpgrade 验证旧 CHECK 升级保留历史记录与自增高水位，重复打开仍可恢复原事实。
func TestPlanOperationsUpgrade(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "records", true: "empty_high_water"}[empty], func(t *testing.T) {
			cfg := &config.Config{DataDir: t.TempDir()}
			db, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.conn.Exec(`DROP TABLE plan_operations`); err != nil {
				t.Fatal(err)
			}
			legacy := strings.Replace(planOperationsSchema, "'claim','prepare_item','status','release'", "'claim','status','release'", 1)
			if _, err = db.conn.Exec(legacy); err != nil {
				t.Fatal(err)
			}
			prior := PlanOperation{OwnerScope: "fixture", PlanID: "10000000-0000-0000-0000-000000000001", RunID: "20000000-0000-0000-0000-000000000001", OwnerID: "40000000-0000-0000-0000-000000000001", RequestID: "60000000-0000-0000-0000-000000000001", Kind: "claim", BodyHash: strings.Repeat("a", 64), Cipher: []byte("fixture-encrypted-bytes")}
			saved, err := db.SavePlanOperation(t.Context(), prior)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.ConfirmPlanOperation(t.Context(), prior.OwnerScope, prior.RequestID, prior.BodyHash); err != nil {
				t.Fatal(err)
			}
			saved.State = "confirmed"
			if _, err = db.conn.Exec(`UPDATE sqlite_sequence SET seq=99 WHERE name='plan_operations'`); err != nil {
				t.Fatal(err)
			}
			if empty {
				if _, err = db.conn.Exec(`DELETE FROM plan_operations`); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if !empty {
				loaded, err := reopened.PlanOperation(t.Context(), prior.OwnerScope, prior.RequestID)
				if err != nil || !reflect.DeepEqual(saved, loaded) {
					t.Fatal("升级丢失原历史请求", err)
				}
			}
			prepared := prior
			prepared.RequestID = "60000000-0000-0000-0000-000000000002"
			prepared.Kind = "prepare_item"
			next, err := reopened.SavePlanOperation(t.Context(), prepared)
			if err != nil || next.Sequence <= 99 {
				t.Fatal("升级丢失落盘顺序或不支持准备请求", err, next.Sequence)
			}
			if _, err = reopened.NextPlanUpdate(t.Context(), prior.OwnerScope); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("准备请求进入后台补传", err)
			}
		})
	}
}
