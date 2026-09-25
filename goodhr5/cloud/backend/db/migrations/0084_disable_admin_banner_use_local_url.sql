-- 本迁移关闭后台广告位默认配置，并将跳转地址改为本地开发地址。
-- 适用于已跑过 0048/0049 迁移的数据库，直接覆盖更新。
UPDATE system_configs
SET config_value = jsonb_set(
  jsonb_set(
    jsonb_set(
      config_value::jsonb,
      '{admin_banner,enabled}',
      'false'
    ),
    '{admin_banner,url}',
    '"http://localhost:3000"'
  ),
  '{admin_banners}',
  (
    SELECT jsonb_agg(
      jsonb_set(
        jsonb_set(item, '{enabled}', 'false'),
        '{url}',
        '"http://localhost:3000"'
      )
    )
    FROM jsonb_array_elements(config_value::jsonb->'admin_banners') AS item
  )
)
WHERE config_key = 'system.app_config';
