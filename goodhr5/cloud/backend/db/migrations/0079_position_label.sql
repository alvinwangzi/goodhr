    -- 岗位模板增加标签字段：选填、不超过 20 字，用于在岗位列表区分同名岗位。
ALTER TABLE positions ADD COLUMN IF NOT EXISTS label TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN positions.label IS '岗位标签，选填不超过20字，用于在岗位列表区分同名岗位';
