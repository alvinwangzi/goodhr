-- 本文件保存 HRPlus 独立执行项的动作激活状态，不用岗位结束状态推断消息任务是否结束。
ALTER TABLE execution_plan_item_runs ADD COLUMN IF NOT EXISTS action_states JSONB NOT NULL DEFAULT '{}';
COMMENT ON COLUMN execution_plan_item_runs.action_states IS '各动作待开始执行中已完成或已停止状态，独立于主执行项结束状态';
