-- 本迁移为用户表增加昵称字段，支持用户在个人信息页面修改显示名称。

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN users.display_name IS '用户昵称，用于后台展示，默认为空';
