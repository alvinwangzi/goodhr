-- 本迁移为 candidate_screenings 表新增"复打招呼"所需的 3 个字段。
-- 用途：记录候选人首次被打招呼的时间、上次复打时间、累计复打次数，
-- 供云端复打名单查询使用（按时间范围 + 间隔下限 + 次数上限过滤）。

ALTER TABLE candidate_screenings ADD COLUMN IF NOT EXISTS greeted_at TIMESTAMPTZ;
COMMENT ON COLUMN candidate_screenings.greeted_at IS '首次打招呼成功时间；打招呼流程上报时写入，upsert 时永不覆盖（避免被自动回复的 source 覆盖影响复打名单判据）';

ALTER TABLE candidate_screenings ADD COLUMN IF NOT EXISTS last_re_greeted_at TIMESTAMPTZ;
COMMENT ON COLUMN candidate_screenings.last_re_greeted_at IS '上次复打招呼成功时间，复打成功上报时写入';

ALTER TABLE candidate_screenings ADD COLUMN IF NOT EXISTS re_greet_count INTEGER NOT NULL DEFAULT 0;
COMMENT ON COLUMN candidate_screenings.re_greet_count IS '该候选人已被复打的累计次数';

-- 复打名单查询高频条件：按平台 + 已打招呼 + 时间范围过滤。
CREATE INDEX IF NOT EXISTS idx_candidate_screenings_regreet_lookup
    ON candidate_screenings (platform, greeted_at DESC)
    WHERE greeted_at IS NOT NULL;
