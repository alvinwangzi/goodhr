-- 本迁移为 HRPlus 单岗位复打提供不可重复记账的收据，不创建执行计划表。
CREATE TABLE IF NOT EXISTS re_greet_receipts (
 operation_id TEXT PRIMARY KEY,
 owner_email TEXT NOT NULL,
 position_id UUID NOT NULL REFERENCES positions(id),
 platform TEXT NOT NULL,
 candidate_id TEXT NOT NULL,
 request_digest TEXT NOT NULL,
 base_count INTEGER NOT NULL,
 base_contact_at TIMESTAMPTZ NOT NULL,
 sent_at TIMESTAMPTZ NOT NULL,
 received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 result_count INTEGER NOT NULL,
 UNIQUE(position_id,platform,candidate_id,base_count,base_contact_at)
);
COMMENT ON TABLE re_greet_receipts IS 'HRPlus 已确认复打发送结果的一次记账收据';
COMMENT ON COLUMN re_greet_receipts.operation_id IS '本次发送意图编号，补传保持原编号';
COMMENT ON COLUMN re_greet_receipts.owner_email IS '经鉴权的上报账号，不信任客户端账号';
COMMENT ON COLUMN re_greet_receipts.position_id IS '归属岗位编号';
COMMENT ON COLUMN re_greet_receipts.platform IS '招聘平台标识';
COMMENT ON COLUMN re_greet_receipts.candidate_id IS '候选人完整平台标识';
COMMENT ON COLUMN re_greet_receipts.request_digest IS '包含身份、发送时间、正文摘要和联系基准的请求摘要';
COMMENT ON COLUMN re_greet_receipts.base_count IS '本次发送前已确认复打次数';
COMMENT ON COLUMN re_greet_receipts.base_contact_at IS '本次发送前已确认联系时间';
COMMENT ON COLUMN re_greet_receipts.sent_at IS '页面确认的实际发送时间，不能替换为补传时间';
COMMENT ON COLUMN re_greet_receipts.received_at IS '云端收到并提交结果的时间';
COMMENT ON COLUMN re_greet_receipts.result_count IS '本次记账后的复打次数';
