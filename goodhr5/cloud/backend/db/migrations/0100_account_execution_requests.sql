-- 本文件保存 HRPlus 账号执行权的原请求结果，防止重试重新占用或迟到释放新任务。
CREATE TABLE IF NOT EXISTS account_execution_requests (
    account_key TEXT NOT NULL,
    request_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('claim','release')),
    body_hash TEXT NOT NULL,
    owner_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(account_key,request_id)
);
COMMENT ON COLUMN account_execution_requests.account_key IS '已认证账号的唯一执行权作用域';
COMMENT ON COLUMN account_execution_requests.request_id IS '原请求幂等编号';
COMMENT ON COLUMN account_execution_requests.kind IS '取得或释放执行权的请求类型';
COMMENT ON COLUMN account_execution_requests.body_hash IS '原请求内容摘要不保存凭证原文';
COMMENT ON COLUMN account_execution_requests.owner_id IS '原请求对应占用者编号';
COMMENT ON COLUMN account_execution_requests.created_at IS '请求首次成功时间';
