-- 本文件为 HRPlus 历史占用编号检查建立索引，已释放编号不能再次取得执行权。
CREATE INDEX IF NOT EXISTS idx_account_execution_request_claim_owner
ON account_execution_requests(owner_id) WHERE kind='claim';
