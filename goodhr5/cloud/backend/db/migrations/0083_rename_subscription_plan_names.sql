-- 本迁移把存量 system_configs 中订阅套餐的旧名称更新为新名称：
-- "永久免费版" → "免费版"、"Plus包月版" → "Plus包月会员"、"Pro包年版" → "Pro包年会员"。
UPDATE system_configs
SET config_value = REPLACE(
  REPLACE(
    REPLACE(config_value::text, '"永久免费版"', '"免费版"'),
    '"Plus包月版"', '"Plus包月会员"'
  ),
  '"Pro包年版"', '"Pro包年会员"'
)::jsonb
WHERE config_key = 'system.subscription_plans';
