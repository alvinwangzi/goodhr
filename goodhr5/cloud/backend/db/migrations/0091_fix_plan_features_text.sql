-- 本迁移修正会员套餐 features 文案：
-- 1. 移除所有套餐中的"多平台账号管理"（功能已下线）
-- 2. Pro 套餐新增"自动索要简历"权益
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'free' THEN
                jsonb_set(item, '{features}',
                    '["关键词筛选", "基础自动打招呼", "每天最多打20个招呼"]'::jsonb)
            WHEN item->>'id' = 'monthly' THEN
                jsonb_set(item, '{features}',
                    '["关键词筛选", "AI筛选与详情分析", "AI自动打招呼"]'::jsonb)
            WHEN item->>'id' = 'yearly' THEN
                jsonb_set(item, '{features}',
                    '["关键词筛选", "AI筛选与详情分析", "AI自动打招呼", "AI自动回复", "自动索要简历"]'::jsonb)
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
