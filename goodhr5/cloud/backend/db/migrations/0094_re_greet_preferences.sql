-- 本迁移为 user_preferences 表新增"复打招呼"相关的 4 个全局配置字段。
-- 复打招呼：对之前打过招呼但未回复的候选人，间隔一段时间后再主动发一次招呼消息。
-- 所有字段均带默认值，老用户无需手动初始化即可按默认行为运行。

ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS re_greet_interval_min INTEGER NOT NULL DEFAULT 30;
COMMENT ON COLUMN user_preferences.re_greet_interval_min IS '复打最小间隔（分钟），与 max 组成随机范围，模拟人工节奏';

ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS re_greet_interval_max INTEGER NOT NULL DEFAULT 50;
COMMENT ON COLUMN user_preferences.re_greet_interval_max IS '复打最大间隔（分钟）';

ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS re_greet_time_range INTEGER NOT NULL DEFAULT 7;
COMMENT ON COLUMN user_preferences.re_greet_time_range IS '复打时间范围（天），只复打最近 N 天内打过招呼的候选人';

ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS re_greet_max_count INTEGER NOT NULL DEFAULT 1;
COMMENT ON COLUMN user_preferences.re_greet_max_count IS '同一候选人最多被复打的次数上限';
