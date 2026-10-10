-- HRPlus 每份原报告仅通知原计划所有者，发送不明不自动重复发信。
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS notification_recipient TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN execution_plan_reports.notification_recipient IS '原计划所有者通知邮箱，不接受客户端任意接收人';
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS notification_token TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN execution_plan_reports.notification_token IS '本次发送领取编号，迟到结果不能覆盖其他发送';
ALTER TABLE execution_plan_reports ADD COLUMN IF NOT EXISTS notification_error TEXT NOT NULL DEFAULT '';
COMMENT ON COLUMN execution_plan_reports.notification_error IS '未配置或发送结果待核对的原因';
