// 本文件在明确的独立 PostgreSQL 中验证 HRPlus M2 新库迁移、中文字段说明和重复岗位约束。
package httpapi

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestExecutionPlanSchemaPostgres 执行真实增量迁移两次，并检查字段备注与重复岗位的独立条目。
func TestExecutionPlanSchemaPostgres(t *testing.T) {
	dsn := os.Getenv("GOODHR_EXECUTION_PLAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要明确的独立 M2 PostgreSQL 测试地址")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Chdir(filepath.Join("..", ".."))
	for i := 0; i < 2; i++ {
		if err := RunMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	var missing int
	err = db.QueryRow(`SELECT COUNT(*) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname IN ('execution_plans','execution_plan_windows','execution_plan_items','execution_plan_runs','execution_plan_item_runs','execution_plan_requests','account_execution_owners','execution_plan_reports') AND a.attnum>0 AND NOT a.attisdropped AND (col_description(c.oid,a.attnum) IS NULL OR col_description(c.oid,a.attnum) !~ '[一-龥]')`).Scan(&missing)
	if err != nil || missing != 0 {
		t.Fatalf("计划字段缺少中文说明 missing=%d err=%v", missing, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO execution_plans(id,user_email,machine_id,name,config) VALUES('10000000-0000-0000-0000-000000000001','fixture@example.com','A','测试计划','{}'); INSERT INTO execution_plan_items(plan_id,item_id,position_id,ordinal,actions) VALUES ('10000000-0000-0000-0000-000000000001','first','20000000-0000-0000-0000-000000000001',0,'["auto_reply"]'),('10000000-0000-0000-0000-000000000001','second','20000000-0000-0000-0000-000000000001',1,'["greeting"]')`)
	if err != nil {
		t.Fatal("重复岗位不能保存", err)
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM execution_plan_items WHERE plan_id='10000000-0000-0000-0000-000000000001'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("执行项被错误合并", err)
	}
}
