-- 本迁移为本地程序启动配置增加可远程调整的控制台打开地址。
UPDATE system_configs
SET config_value = jsonb_set(
	config_value,
	'{local_agent_console_url}',
	to_jsonb(COALESCE(NULLIF(config_value ->> 'local_agent_console_url', ''), 'https://www.xx.com/admin')),
	true
),
updated_at = now()
WHERE config_key = 'system.onboarding_config';
