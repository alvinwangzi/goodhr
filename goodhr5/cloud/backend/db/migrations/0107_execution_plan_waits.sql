-- 本迁移保存 HRPlus 执行电脑的原排队事实，排队不授予运行许可。
CREATE TABLE IF NOT EXISTS execution_plan_waits (
    plan_id UUID NOT NULL REFERENCES execution_plans(id),
    request_id UUID NOT NULL,
    activation_id UUID NOT NULL,
    config_version BIGINT NOT NULL CHECK(config_version>0),
    machine_id TEXT NOT NULL,
    triggered_at TIMESTAMPTZ NOT NULL,
	queued_at TIMESTAMPTZ,
    PRIMARY KEY(plan_id,request_id)
);
COMMENT ON COLUMN execution_plan_waits.plan_id IS '原计划编号';
COMMENT ON COLUMN execution_plan_waits.request_id IS '本地原窗口排队请求编号，重试不改变';
COMMENT ON COLUMN execution_plan_waits.activation_id IS '原计划启用批次';
COMMENT ON COLUMN execution_plan_waits.config_version IS '原配置版本';
COMMENT ON COLUMN execution_plan_waits.machine_id IS '登记原排队事实的指定电脑';
COMMENT ON COLUMN execution_plan_waits.triggered_at IS '本地持久保存的原定触发时间，非上报时间';
CREATE INDEX IF NOT EXISTS idx_execution_plan_waits_activation ON execution_plan_waits(plan_id,activation_id,triggered_at);

-- 排队首次保存及手动/计划账号占用变化均提示网页重读，重试不写入便不会重复通知。
CREATE OR REPLACE FUNCTION hrplus_notify_plan_wait_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE scope_key text;
BEGIN
    SELECT md5(COALESCE(tenant_id::text,'') || E'\n' || user_email) INTO scope_key FROM execution_plans WHERE id=COALESCE(NEW.plan_id,OLD.plan_id);
    IF scope_key IS NOT NULL THEN PERFORM pg_notify('hrplus_plan_changes',scope_key); END IF;
    RETURN NULL;
END;
$$;
COMMENT ON FUNCTION hrplus_notify_plan_wait_change() IS '原排队事实提交后只发送所有者作用域摘要';
DROP TRIGGER IF EXISTS hrplus_plan_wait_change ON execution_plan_waits;
CREATE TRIGGER hrplus_plan_wait_change AFTER INSERT OR UPDATE OR DELETE ON execution_plan_waits FOR EACH ROW EXECUTE FUNCTION hrplus_notify_plan_wait_change();

CREATE OR REPLACE FUNCTION hrplus_notify_account_plan_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE scope_key text;
BEGIN
    FOR scope_key IN SELECT DISTINCT md5(COALESCE(tenant_id::text,'') || E'\n' || user_email) FROM execution_plans WHERE user_email=COALESCE(NEW.account_key,OLD.account_key) AND deleted_at IS NULL LOOP
        PERFORM pg_notify('hrplus_plan_changes',scope_key);
    END LOOP;
    RETURN NULL;
END;
$$;
COMMENT ON FUNCTION hrplus_notify_account_plan_change() IS '账号占用提交后按原用户的计划团队提示重读，不回传占用凭证';
DROP TRIGGER IF EXISTS hrplus_account_plan_change ON account_execution_owners;
CREATE TRIGGER hrplus_account_plan_change AFTER INSERT OR UPDATE OR DELETE ON account_execution_owners FOR EACH ROW EXECUTE FUNCTION hrplus_notify_account_plan_change();
