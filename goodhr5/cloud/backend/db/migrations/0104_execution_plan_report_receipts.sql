-- HRPlus 原报告摘要与当前同步状态独立保存，通知状态仍由后续通知流程维护。
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS body_hash TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN execution_plan_reports.body_hash IS '首次原报告内容摘要，拒绝重复上报改写';
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS sync_state TEXT NOT NULL DEFAULT 'pending';
COMMENT ON COLUMN execution_plan_reports.sync_state IS '当前原运行同步状态，不替换首次报告内容';
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
COMMENT ON COLUMN execution_plan_reports.updated_at IS '最近同步状态更新时间';
