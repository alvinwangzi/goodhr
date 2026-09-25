-- 本迁移更新订阅套餐价格：Plus 包月 99 元（划线 120），Pro 包年 999 元（划线 1300）。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'monthly' THEN
                jsonb_set(jsonb_set(item, '{original_price}', '120'), '{discount_amount}', '21')
            WHEN item->>'id' = 'yearly' THEN
                jsonb_set(jsonb_set(item, '{original_price}', '1300'), '{discount_amount}', '301')
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
