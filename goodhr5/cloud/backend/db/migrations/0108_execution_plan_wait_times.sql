-- 本迁移补齐 HRPlus 真实入队时间，旧排队记录保持未知而不使用迁移时刻。
ALTER TABLE execution_plan_waits ADD COLUMN IF NOT EXISTS queued_at TIMESTAMPTZ;
COMMENT ON COLUMN execution_plan_waits.queued_at IS '实际首次进入本地队列的时间，旧记录没有此值时留空';
