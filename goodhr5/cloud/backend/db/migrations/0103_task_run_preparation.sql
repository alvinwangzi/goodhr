-- 本文件允许 HRPlus 已领取但尚未开始页面操作的任务保存空开始时间，既有任务时间保留。
ALTER TABLE task_runs ALTER COLUMN started_at DROP NOT NULL;
COMMENT ON COLUMN task_runs.started_at IS '实际开始时间，starting 准备阶段为空';
COMMENT ON COLUMN task_runs.status IS '准备中starting运行中running或完成停止失败状态';
