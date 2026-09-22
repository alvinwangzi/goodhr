-- 恢复执行任务记录表 task_runs：每次岗位启动创建一条运行记录，用于后台展示执行任务和本次名单。
-- 说明：task_runs 曾在 0066 中随"任务"概念一起删除，本迁移以执行任务的新口径重建；
-- 候选人事件表补回 task_id，把每次运行的打招呼、索要简历等事件归组到对应运行。

CREATE TABLE IF NOT EXISTS task_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position_id UUID REFERENCES positions(id) ON DELETE SET NULL,
    platform_id TEXT NOT NULL DEFAULT '',
    task_type TEXT NOT NULL DEFAULT 'greeting',
    machine_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    scanned_count INTEGER NOT NULL DEFAULT 0,
    greeted_count INTEGER NOT NULL DEFAULT 0,
    resume_requested_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

COMMENT ON TABLE task_runs IS '执行任务运行记录表，每次岗位启动创建一条，保存本次运行状态和统计';
COMMENT ON COLUMN task_runs.tenant_id IS '所属团队ID';
COMMENT ON COLUMN task_runs.user_id IS '发起运行的用户ID';
COMMENT ON COLUMN task_runs.position_id IS '关联岗位ID';
COMMENT ON COLUMN task_runs.platform_id IS '运行平台标识，如boss';
COMMENT ON COLUMN task_runs.task_type IS '任务类型，greeting为岗位运行扫描打招呼，auto_reply为AI对答';
COMMENT ON COLUMN task_runs.machine_id IS '发起运行的本地设备机器码';
COMMENT ON COLUMN task_runs.status IS '运行状态，running运行中/completed已完成/stopped已停止/failed失败';
COMMENT ON COLUMN task_runs.scanned_count IS '本次运行保存的候选人数量';
COMMENT ON COLUMN task_runs.greeted_count IS '本次运行打招呼成功数量';
COMMENT ON COLUMN task_runs.resume_requested_count IS '本次运行索要简历数量';
COMMENT ON COLUMN task_runs.skipped_count IS '本次运行跳过的候选人数量';
COMMENT ON COLUMN task_runs.failed_count IS '本次运行处理失败数量';
COMMENT ON COLUMN task_runs.error_message IS '运行失败或停止原因说明';
COMMENT ON COLUMN task_runs.created_at IS '记录创建时间';
COMMENT ON COLUMN task_runs.started_at IS '运行开始时间';
COMMENT ON COLUMN task_runs.finished_at IS '运行结束时间';

CREATE INDEX IF NOT EXISTS idx_task_runs_tenant_created_at
    ON task_runs(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_runs_position_created_at
    ON task_runs(position_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_runs_status
    ON task_runs(status);

-- 候选人事件表补回任务运行外键：事件按运行归组，用于按任务查询打招呼和索要简历名单。
ALTER TABLE candidate_events
    ADD COLUMN IF NOT EXISTS task_id UUID REFERENCES task_runs(id) ON DELETE SET NULL;
COMMENT ON COLUMN candidate_events.task_id IS '关联执行任务运行ID，标记事件发生在哪次运行';

CREATE INDEX IF NOT EXISTS idx_candidate_events_task_created_at
    ON candidate_events(task_id, created_at DESC);

-- 触达上下文记录候选人针对岗位的最新简历请求时间，用于简历库状态展示。
ALTER TABLE candidate_engagements
    ADD COLUMN IF NOT EXISTS resume_requested_at TIMESTAMPTZ;
COMMENT ON COLUMN candidate_engagements.resume_requested_at IS '最近一次向该候选人索要简历的时间';
