-- 本迁移把免费版每日打招呼上限从 100 调整为 20。
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{free_daily_greet_limit}', '20'::jsonb)
WHERE config_key = 'system.app_config';
