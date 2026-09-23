-- 候选人扫描记录表：打招呼流程与自动回复流程共享的筛选状态。
-- 打招呼流程写入 AI 评分 >= 50 的候选人；自动回复流程写入所有遇到的会话候选人。
CREATE TABLE IF NOT EXISTS candidate_screenings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    position_id UUID NOT NULL REFERENCES positions(id) ON DELETE CASCADE, -- 关联岗位
    platform VARCHAR(50) NOT NULL,                                        -- 平台标识（boss/liepin/zhaopin/hliepin）
    platform_candidate_id VARCHAR(255) NOT NULL,                          -- 平台侧候选人唯一标识
    candidate_name VARCHAR(100) NOT NULL DEFAULT '',                      -- 候选人姓名
    score INTEGER NOT NULL DEFAULT 0,                                     -- AI 评分
    status VARCHAR(20) NOT NULL DEFAULT 'screened',                       -- passed=过打招呼阈值 / screened=过入库线但没过打招呼阈值
    resume_status VARCHAR(20) NOT NULL DEFAULT 'none',                    -- none / requested / received
    source VARCHAR(20) NOT NULL DEFAULT 'greeting',                       -- greeting=打招呼流程 / auto_reply=自动回复流程
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),                        -- 首次扫描时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()                         -- 最后更新时间
);

COMMENT ON TABLE candidate_screenings IS '候选人扫描记录表，打招呼与自动回复共享的筛选状态';

-- 同一岗位同一平台同一候选人只保留一条记录。
CREATE UNIQUE INDEX IF NOT EXISTS idx_candidate_screenings_unique
    ON candidate_screenings (position_id, platform, platform_candidate_id);

-- 自动回复时按岗位+平台+候选人快速查找。
CREATE INDEX IF NOT EXISTS idx_candidate_screenings_lookup
    ON candidate_screenings (position_id, platform, platform_candidate_id);

-- 后台按岗位分页查看。
CREATE INDEX IF NOT EXISTS idx_candidate_screenings_position
    ON candidate_screenings (position_id, created_at DESC);
