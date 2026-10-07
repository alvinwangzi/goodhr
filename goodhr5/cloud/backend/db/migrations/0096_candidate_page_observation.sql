-- 本迁移保存招聘平台页面核实的沟通事实；核对时间不代表实际打招呼时间。
ALTER TABLE candidate_screenings ADD COLUMN IF NOT EXISTS contact_observed BOOLEAN NOT NULL DEFAULT false;
COMMENT ON COLUMN candidate_screenings.contact_observed IS '从平台页面核实已经存在沟通，用于避免重复首次打招呼；不据此生成 greeted_at';
ALTER TABLE candidate_screenings ADD COLUMN IF NOT EXISTS platform_observed_at TIMESTAMPTZ;
COMMENT ON COLUMN candidate_screenings.platform_observed_at IS '最近一次核对平台页面状态的时间，不代表实际沟通或收取简历时间';
