-- 本迁移把 HRPlus 旧手动岗位的真实运行切换通知计划页，同状态统计心跳不产生通知。
CREATE OR REPLACE FUNCTION hrplus_notify_legacy_position_plan_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_email text; scope_key text;
BEGIN
    IF TG_OP='UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NULL; END IF;
    IF COALESCE(NEW.status,'')<>'running' AND COALESCE(OLD.status,'')<>'running' THEN RETURN NULL; END IF;
    SELECT email INTO target_email FROM users WHERE id=COALESCE(NEW.user_id,OLD.user_id);
    FOR scope_key IN SELECT DISTINCT md5(COALESCE(tenant_id::text,'') || E'\n' || user_email) FROM execution_plans WHERE user_email=target_email AND deleted_at IS NULL LOOP
        PERFORM pg_notify('hrplus_plan_changes',scope_key);
    END LOOP;
    RETURN NULL;
END;
$$;
COMMENT ON FUNCTION hrplus_notify_legacy_position_plan_change() IS '旧手动岗位运行切换提交后只提示所属账号重读计划事实';
DROP TRIGGER IF EXISTS hrplus_legacy_position_plan_change ON positions;
CREATE TRIGGER hrplus_legacy_position_plan_change AFTER INSERT OR UPDATE OR DELETE ON positions FOR EACH ROW EXECUTE FUNCTION hrplus_notify_legacy_position_plan_change();
