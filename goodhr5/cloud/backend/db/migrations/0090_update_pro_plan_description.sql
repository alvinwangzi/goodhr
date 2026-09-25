-- 本迁移更新 Plus 和 Pro 会员描述文案，明确索要简历为 PRO 专享能力。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'monthly' THEN
                jsonb_set(item, '{description}', '"适合日常招聘使用，包含 AI 筛选和自动打招呼，不包含自动回复和索要简历。"')
            WHEN item->>'id' = 'yearly' THEN
                jsonb_set(item, '{description}', '"完整开放现有会员能力，包含自动回复和索要简历。"')
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
