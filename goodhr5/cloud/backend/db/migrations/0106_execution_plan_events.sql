-- 本迁移在 HRPlus 计划事实提交后通知网页；跨云端进程只发送作用域摘要，不含凭证或候选人资料。
CREATE OR REPLACE FUNCTION hrplus_notify_plan_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_plan uuid;
    scope_key text;
BEGIN
    IF TG_TABLE_NAME = 'execution_plans' THEN
        target_plan := COALESCE(NEW.id, OLD.id);
    ELSIF TG_TABLE_NAME = 'execution_plan_runs' THEN
        target_plan := COALESCE(NEW.plan_id, OLD.plan_id);
    ELSE
        SELECT plan_id INTO target_plan FROM execution_plan_runs WHERE id = COALESCE(NEW.run_id, OLD.run_id);
    END IF;
    SELECT md5(COALESCE(tenant_id::text, '') || E'\n' || user_email) INTO scope_key FROM execution_plans WHERE id = target_plan;
    IF scope_key IS NOT NULL THEN
        PERFORM pg_notify('hrplus_plan_changes', scope_key);
    END IF;
    RETURN NULL;
END;
$$;
COMMENT ON FUNCTION hrplus_notify_plan_change() IS '事务提交后发送计划所有者作用域提示，网页收到后重读原事实';
DROP TRIGGER IF EXISTS hrplus_plan_change ON execution_plans;
CREATE TRIGGER hrplus_plan_change AFTER INSERT OR UPDATE OR DELETE ON execution_plans FOR EACH ROW EXECUTE FUNCTION hrplus_notify_plan_change();
DROP TRIGGER IF EXISTS hrplus_plan_run_change ON execution_plan_runs;
CREATE TRIGGER hrplus_plan_run_change AFTER INSERT OR UPDATE OR DELETE ON execution_plan_runs FOR EACH ROW EXECUTE FUNCTION hrplus_notify_plan_change();
DROP TRIGGER IF EXISTS hrplus_plan_item_run_change ON execution_plan_item_runs;
CREATE TRIGGER hrplus_plan_item_run_change AFTER INSERT OR UPDATE OR DELETE ON execution_plan_item_runs FOR EACH ROW EXECUTE FUNCTION hrplus_notify_plan_change();
DROP TRIGGER IF EXISTS hrplus_plan_report_change ON execution_plan_reports;
CREATE TRIGGER hrplus_plan_report_change AFTER INSERT OR UPDATE OR DELETE ON execution_plan_reports FOR EACH ROW EXECUTE FUNCTION hrplus_notify_plan_change();
