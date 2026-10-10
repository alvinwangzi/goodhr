-- 本迁移保存 HRPlus 原邮件发送开始时间，报告补传不能延长同一次发送的恢复期限。
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS notification_started_at TIMESTAMPTZ;
COMMENT ON COLUMN execution_plan_reports.notification_started_at IS '本次原邮件发送领取时刻，历史未知时留空，结果不明不自动重发';
