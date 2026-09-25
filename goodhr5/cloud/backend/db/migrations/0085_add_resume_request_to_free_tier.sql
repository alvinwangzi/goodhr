-- 本迁移把免费版套餐的 features 列表新增"打招呼后索要简历"权益，放在"基础自动打招呼"后面。
UPDATE system_configs
SET config_value = REPLACE(
  config_value::text,
  '"基础自动打招呼", "每天最多打20个招呼"',
  '"基础自动打招呼", "打招呼后索要简历", "每天最多打20个招呼"'
)::jsonb
WHERE config_key = 'system.subscription_plans';
