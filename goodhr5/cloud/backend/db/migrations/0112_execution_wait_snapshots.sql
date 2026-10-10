-- 本迁移固定 HRPlus 原排队日的配置，未开始日末报告不能使用后来编辑的计划。
CREATE TABLE IF NOT EXISTS execution_plan_wait_snapshots (
    plan_id UUID NOT NULL,
    request_id UUID NOT NULL,
    execution_date DATE NOT NULL,
    snapshot JSONB NOT NULL,
    PRIMARY KEY(plan_id,request_id),
    FOREIGN KEY(plan_id,request_id) REFERENCES execution_plan_waits(plan_id,request_id)
);
COMMENT ON COLUMN execution_plan_wait_snapshots.plan_id IS '原排队计划编号';
COMMENT ON COLUMN execution_plan_wait_snapshots.request_id IS '原窗口排队请求编号';
COMMENT ON COLUMN execution_plan_wait_snapshots.execution_date IS '原配置时区中的执行日期';
COMMENT ON COLUMN execution_plan_wait_snapshots.snapshot IS '原排队时的冻结配置，不包含令牌或执行凭证';
-- 旧排队只在当前配置仍是同一原版本时补齐；不同版本不能猜测原配置。
INSERT INTO execution_plan_wait_snapshots(plan_id,request_id,execution_date,snapshot)
SELECT w.plan_id,w.request_id,(w.triggered_at AT TIME ZONE (p.config->'schedule'->>'timezone'))::date,p.config
FROM execution_plan_waits w JOIN execution_plans p ON p.id=w.plan_id
WHERE p.config_version=w.config_version AND p.activation_id=w.activation_id
ON CONFLICT(plan_id,request_id) DO NOTHING;
