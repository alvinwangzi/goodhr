-- 本迁移为用户表增加密码哈希字段，支持密码登录。
-- 密码使用 bcrypt 哈希存储，未设置密码的用户该字段为 NULL。

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash TEXT;

COMMENT ON COLUMN users.password_hash IS 'bcrypt 哈希后的密码，NULL 表示未设置密码';
