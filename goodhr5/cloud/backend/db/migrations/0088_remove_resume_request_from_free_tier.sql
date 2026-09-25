-- 本迁移从免费版 features 中移除"打招呼后索要简历"，并修正招呼上限文案 100→20。
-- 索要简历依赖聊天页能力，属于 PRO 专享，不应出现在免费版权益中。
UPDATE system_configs
SET config_value = (
    SELECT jsonb_agg(
        CASE
            WHEN item->>'id' = 'free' THEN
                jsonb_set(
                    item,
                    '{features}',
                    (
                        SELECT COALESCE(jsonb_agg(
                            CASE
                                WHEN f #>> '{}' = '打招呼后索要简历' THEN NULL
                                WHEN f #>> '{}' = '每天最多打100个招呼' THEN '"每天最多打20个招呼"'::jsonb
                                ELSE f
                            END
                        ) FILTER (WHERE f #>> '{}' != '打招呼后索要简历'), '[]'::jsonb)
                        FROM jsonb_array_elements(item->'features') AS f
                    )
                )
            ELSE item
        END
    )
    FROM jsonb_array_elements(config_value) AS item
)
WHERE config_key = 'system.subscription_plans';
