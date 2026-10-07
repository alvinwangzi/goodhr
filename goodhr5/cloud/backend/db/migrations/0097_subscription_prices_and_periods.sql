-- 本迁移调整新购会员套餐：包月 99 元（划线 199）、30 天；包年 1988 元（划线 2388）、365 天。
-- 只修改套餐配置，不重写历史订单或现有会员到期时间，保留套餐权限及其他字段。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE item->>'member_type'
            WHEN 'plus' THEN jsonb_set(jsonb_set(jsonb_set(item, '{original_price}', '199'), '{discount_amount}', '100'), '{duration_days}', '30')
            WHEN 'pro' THEN jsonb_set(jsonb_set(jsonb_set(item, '{original_price}', '2388'), '{discount_amount}', '400'), '{duration_days}', '365')
            ELSE item
        END ORDER BY ordinal
    )
    FROM jsonb_array_elements(config_value) WITH ORDINALITY AS plans(item, ordinal)
)
WHERE config_key = 'system.subscription_plans';
