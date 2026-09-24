-- 本文件为简历库补充独立的索要进度，保留已有候选人及其触达状态，不修改历史简历正文。
ALTER TABLE candidate_engagements
    ADD COLUMN IF NOT EXISTS resume_state TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS resume_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS resume_updated_at TIMESTAMPTZ;

COMMENT ON COLUMN candidate_engagements.resume_state IS '简历进度：空为旧记录，pending待索要，requested已索要，received已收到，downloaded已下载';
COMMENT ON COLUMN candidate_engagements.resume_error IS '最近简历操作失败原因；失败不回退已确认进度';
COMMENT ON COLUMN candidate_engagements.resume_updated_at IS '本地已确认简历进度的更新时间，用于拒绝迟到和重复报告';

UPDATE candidate_engagements SET resume_state = 'requested'
WHERE resume_state = '' AND resume_requested_at IS NOT NULL;
