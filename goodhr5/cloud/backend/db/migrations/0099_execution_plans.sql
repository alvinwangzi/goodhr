-- 本增量迁移建立 HRPlus M2 编排及账号占用数据，保留原岗位、候选人和 M1 收据。
CREATE TABLE IF NOT EXISTS execution_plans (
    id UUID PRIMARY KEY,
    tenant_id UUID,
    user_email TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    name TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'stopped' CHECK(state IN ('stopped','enabled','disabled')),
    config_version BIGINT NOT NULL DEFAULT 1 CHECK(config_version>0),
    state_sequence BIGINT NOT NULL DEFAULT 1 CHECK(state_sequence>0),
    activation_id UUID,
    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    config JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
COMMENT ON COLUMN execution_plans.id IS '计划编号';
COMMENT ON COLUMN execution_plans.tenant_id IS '所有团队编号';
COMMENT ON COLUMN execution_plans.user_email IS '所有者邮箱';
COMMENT ON COLUMN execution_plans.machine_id IS '指定执行电脑编号';
COMMENT ON COLUMN execution_plans.name IS '计划名称';
COMMENT ON COLUMN execution_plans.state IS '长期计划状态';
COMMENT ON COLUMN execution_plans.config_version IS '配置版本';
COMMENT ON COLUMN execution_plans.state_sequence IS '计划状态单调序号';
COMMENT ON COLUMN execution_plans.activation_id IS '当前启用批次编号';
COMMENT ON COLUMN execution_plans.stop_requested IS '持久停止意图';
COMMENT ON COLUMN execution_plans.config IS '编排配置不含登录凭证';
COMMENT ON COLUMN execution_plans.created_at IS '创建时间';
COMMENT ON COLUMN execution_plans.updated_at IS '更新时间';
COMMENT ON COLUMN execution_plans.deleted_at IS '软删除时间保留运行历史';
CREATE TABLE IF NOT EXISTS execution_plan_windows (
    plan_id UUID NOT NULL REFERENCES execution_plans(id),
    ordinal INTEGER NOT NULL CHECK(ordinal>=0),
    start_minute INTEGER NOT NULL CHECK(start_minute>=0),
    end_minute INTEGER NOT NULL CHECK(end_minute<=1440) ,
    PRIMARY KEY(plan_id,ordinal),
    CHECK(start_minute<end_minute)
);
COMMENT ON COLUMN execution_plan_windows.plan_id IS '所属计划编号';
COMMENT ON COLUMN execution_plan_windows.ordinal IS '连续窗口顺序';
COMMENT ON COLUMN execution_plan_windows.start_minute IS '当天名义开始分钟';
COMMENT ON COLUMN execution_plan_windows.end_minute IS '当天名义结束分钟';
CREATE TABLE IF NOT EXISTS execution_plan_items (
    plan_id UUID NOT NULL REFERENCES execution_plans(id),
    item_id TEXT NOT NULL,
    position_id UUID NOT NULL,
    ordinal INTEGER NOT NULL CHECK(ordinal>=0),
    actions JSONB NOT NULL,
    prioritize_reply BOOLEAN NOT NULL DEFAULT FALSE ,
    PRIMARY KEY(plan_id,item_id),
    UNIQUE(plan_id,ordinal)
);
COMMENT ON COLUMN execution_plan_items.plan_id IS '所属计划编号';
COMMENT ON COLUMN execution_plan_items.item_id IS '独立编排项编号';
COMMENT ON COLUMN execution_plan_items.position_id IS '岗位编号允许重复';
COMMENT ON COLUMN execution_plan_items.ordinal IS '连续执行项顺序';
COMMENT ON COLUMN execution_plan_items.actions IS '启用动作集合';
COMMENT ON COLUMN execution_plan_items.prioritize_reply IS '是否优先回复';
CREATE TABLE IF NOT EXISTS execution_plan_runs (
    id UUID PRIMARY KEY,
    plan_id UUID NOT NULL REFERENCES execution_plans(id),
    activation_id UUID NOT NULL,
    execution_date DATE NOT NULL,
    config_version BIGINT NOT NULL,
    snapshot JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    sequence BIGINT NOT NULL DEFAULT 1,
    current_item INTEGER NOT NULL DEFAULT 0,
    owner_id UUID,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    end_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW() ,
    UNIQUE(plan_id,execution_date,activation_id)
);
COMMENT ON COLUMN execution_plan_runs.id IS '当日运行编号';
COMMENT ON COLUMN execution_plan_runs.plan_id IS '所属计划编号';
COMMENT ON COLUMN execution_plan_runs.activation_id IS '原启用批次编号';
COMMENT ON COLUMN execution_plan_runs.execution_date IS '计划时区中的执行日期';
COMMENT ON COLUMN execution_plan_runs.config_version IS '使用的配置版本';
COMMENT ON COLUMN execution_plan_runs.snapshot IS '不可变编排快照不含凭证';
COMMENT ON COLUMN execution_plan_runs.state IS '本次运行状态';
COMMENT ON COLUMN execution_plan_runs.sequence IS '递增状态序号';
COMMENT ON COLUMN execution_plan_runs.current_item IS '主执行项游标';
COMMENT ON COLUMN execution_plan_runs.owner_id IS '当前或最近账号占用编号';
COMMENT ON COLUMN execution_plan_runs.started_at IS '实际开始时间';
COMMENT ON COLUMN execution_plan_runs.finished_at IS '实际结束时间';
COMMENT ON COLUMN execution_plan_runs.end_reason IS '结束或受阻原因';
COMMENT ON COLUMN execution_plan_runs.created_at IS '登记时间';
CREATE TABLE IF NOT EXISTS execution_plan_item_runs (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES execution_plan_runs(id),
    item_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    task_run_id UUID REFERENCES task_runs(id) ON DELETE SET NULL,
    snapshot JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    counts JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW() ,
    UNIQUE(run_id,item_id)
);
COMMENT ON COLUMN execution_plan_item_runs.id IS '执行项运行编号';
COMMENT ON COLUMN execution_plan_item_runs.run_id IS '父计划运行编号';
COMMENT ON COLUMN execution_plan_item_runs.item_id IS '原独立编排项编号';
COMMENT ON COLUMN execution_plan_item_runs.ordinal IS '主执行顺序';
COMMENT ON COLUMN execution_plan_item_runs.task_run_id IS '关联单岗位任务记录';
COMMENT ON COLUMN execution_plan_item_runs.snapshot IS '执行项快照不含凭证';
COMMENT ON COLUMN execution_plan_item_runs.state IS '执行项状态';
COMMENT ON COLUMN execution_plan_item_runs.counts IS '各动作真实数量与未知结果';
COMMENT ON COLUMN execution_plan_item_runs.updated_at IS '最近更新时间';
CREATE TABLE IF NOT EXISTS execution_plan_requests (
    plan_id UUID NOT NULL REFERENCES execution_plans(id),
    request_id UUID NOT NULL,
    kind TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW() ,
    PRIMARY KEY(plan_id,request_id)
);
COMMENT ON COLUMN execution_plan_requests.plan_id IS '所属计划编号';
COMMENT ON COLUMN execution_plan_requests.request_id IS '明确请求幂等编号';
COMMENT ON COLUMN execution_plan_requests.kind IS '开始停止或领取请求类型';
COMMENT ON COLUMN execution_plan_requests.body_hash IS '不可变请求摘要';
COMMENT ON COLUMN execution_plan_requests.result IS '原请求返回元数据不含登录令牌';
COMMENT ON COLUMN execution_plan_requests.created_at IS '首次请求时间';
CREATE TABLE IF NOT EXISTS account_execution_owners (
    account_key TEXT PRIMARY KEY,
    user_email TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    owner_type TEXT NOT NULL CHECK(owner_type IN ('manual','plan')),
    owner_id UUID NOT NULL UNIQUE,
    credential_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('starting','running','releasing')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON COLUMN account_execution_owners.account_key IS '账号级唯一占用键';
COMMENT ON COLUMN account_execution_owners.user_email IS '占用账号邮箱';
COMMENT ON COLUMN account_execution_owners.machine_id IS '获准执行电脑编号';
COMMENT ON COLUMN account_execution_owners.owner_type IS '手动或计划占用类型';
COMMENT ON COLUMN account_execution_owners.owner_id IS '独立占用编号';
COMMENT ON COLUMN account_execution_owners.credential_hash IS '占用凭证摘要不保存登录令牌';
COMMENT ON COLUMN account_execution_owners.state IS '实际占用或收尾状态';
COMMENT ON COLUMN account_execution_owners.created_at IS '取得占用时间';
CREATE TABLE IF NOT EXISTS execution_plan_reports (
    run_id UUID PRIMARY KEY REFERENCES execution_plan_runs(id),
    summary JSONB NOT NULL,
    notification_state TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON COLUMN execution_plan_reports.run_id IS '对应原运行编号';
COMMENT ON COLUMN execution_plan_reports.summary IS '日末或停止报告摘要';
COMMENT ON COLUMN execution_plan_reports.notification_state IS '通知状态';
COMMENT ON COLUMN execution_plan_reports.attempts IS '发送尝试次数';
COMMENT ON COLUMN execution_plan_reports.next_retry_at IS '下次重试时间';
COMMENT ON COLUMN execution_plan_reports.created_at IS '报告生成时间';
CREATE INDEX IF NOT EXISTS idx_execution_plan_device ON execution_plans(user_email,machine_id,state) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_execution_plan_run_history ON execution_plan_runs(plan_id,execution_date DESC);
CREATE INDEX IF NOT EXISTS idx_execution_plan_report_retry ON execution_plan_reports(notification_state,next_retry_at);
