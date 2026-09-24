-- 本迁移将已有数据中的会员类型 max 统一更名为 pro，配合代码层常量 memberTypeMax→memberTypePro 的改名。

-- 更新用户订阅记录中的 member_type
UPDATE users
SET subscription = jsonb_set(subscription, '{member_type}', '"pro"'::jsonb)
WHERE subscription->>'member_type' = 'max';

-- 更新支付订单的 member_type
UPDATE payment_orders
SET member_type = 'pro'
WHERE member_type = 'max';

-- 更新支付订单的升级来源类型
UPDATE payment_orders
SET upgrade_from_member_type = 'pro'
WHERE upgrade_from_member_type = 'max';

-- 更新权益发放记录的 member_type
UPDATE subscription_order_grants
SET member_type = 'pro'
WHERE member_type = 'max';

-- 更新系统配置中套餐 JSON 的 member_type 值
UPDATE system_configs
SET config_value = REPLACE(config_value::text, '"member_type": "max"', '"member_type": "pro"')::jsonb
WHERE config_key = 'system.subscription_plans';
