-- 本迁移为已存在的 system.onboarding_config 补充 local_agent 字段（若尚未设置）。
-- 种子数据定义了 local_agent，但老库通过 COALESCE 迁移创建时未包含该字段。
UPDATE system_configs
SET config_value = jsonb_set(
    config_value,
    '{local_agent}',
    '[{"version": "0.1.1", "url_win": "", "url_mac": "", "sha256": "", "note": "HRPlus 本地程序安装包"}]'::jsonb,
    true
)
WHERE config_key = 'system.onboarding_config'
  AND (config_value->'local_agent' IS NULL OR config_value->'local_agent' = 'null'::jsonb);
