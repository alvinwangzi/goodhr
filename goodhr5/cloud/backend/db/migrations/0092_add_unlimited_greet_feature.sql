-- 本迁移在 Plus 和 Pro 套餐 features 中新增"每天打招呼无上限"，置于"AI自动打招呼"之前。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'monthly' THEN
                jsonb_set(item, '{features}',
                    '["关键词筛选", "AI筛选与详情分析", "每天打招呼无上限", "AI自动打招呼"]'::jsonb)
            WHEN item->>'id' = 'yearly' THEN
                jsonb_set(item, '{features}',
                    '["关键词筛选", "AI筛选与详情分析", "每天打招呼无上限", "AI自动打招呼", "AI自动回复", "自动索要简历"]'::jsonb)
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
