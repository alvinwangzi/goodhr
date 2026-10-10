-- 本迁移保存 HRPlus 原执行项同步日志，本地原编号重试不能替换内容。
CREATE TABLE IF NOT EXISTS execution_plan_item_logs (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES execution_plan_runs(id),
    item_run_id UUID NOT NULL REFERENCES execution_plan_item_runs(id),
    task_run_id UUID NOT NULL,
    local_run_id TEXT NOT NULL,
    source_id BIGINT NOT NULL CHECK(source_id>0),
    position_id TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    level TEXT NOT NULL,
    message TEXT NOT NULL,
    source_created_at TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    UNIQUE(run_id,item_run_id,local_run_id,source_id)
);
COMMENT ON COLUMN execution_plan_item_logs.id IS '云端分页流水编号';
COMMENT ON COLUMN execution_plan_item_logs.run_id IS '原父计划运行编号';
COMMENT ON COLUMN execution_plan_item_logs.item_run_id IS '原独立执行项运行编号';
COMMENT ON COLUMN execution_plan_item_logs.task_run_id IS '原云端任务编号';
COMMENT ON COLUMN execution_plan_item_logs.local_run_id IS '原本地检查点编号';
COMMENT ON COLUMN execution_plan_item_logs.source_id IS '原本地日志流水，重试不更改';
COMMENT ON COLUMN execution_plan_item_logs.position_id IS '原岗位编号，不作为唯一日志归属';
COMMENT ON COLUMN execution_plan_item_logs.machine_id IS '原执行电脑编号';
COMMENT ON COLUMN execution_plan_item_logs.level IS '实际日志级别';
COMMENT ON COLUMN execution_plan_item_logs.message IS '原候选人或步骤日志内容';
COMMENT ON COLUMN execution_plan_item_logs.source_created_at IS '原日志生成时间文本，保留原精度';
COMMENT ON COLUMN execution_plan_item_logs.body_hash IS '原来源和内容摘要，不允许原编号替换内容';
CREATE INDEX IF NOT EXISTS idx_execution_plan_item_logs_page ON execution_plan_item_logs(run_id,item_run_id,id DESC);
