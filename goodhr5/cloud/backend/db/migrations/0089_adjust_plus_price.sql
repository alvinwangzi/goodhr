-- 本迁移调整 Plus 包月价格为 19.9 元（划线 49.9），因 Plus 权益已不含索要简历和自动回复。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'monthly' THEN
                jsonb_set(jsonb_set(item, '{original_price}', '49.9'), '{discount_amount}', '30')
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
