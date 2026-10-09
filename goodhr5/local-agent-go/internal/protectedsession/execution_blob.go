// 本文件复用 Windows 当前用户 DPAPI 保护 HRPlus 原执行请求，并将密文绑定已核对的账号作用域。
package protectedsession

import (
	"encoding/json"
	"errors"
)

// executionEnvelope 防止会话密文或其他账号的执行请求被当作当前执行请求解密。
type executionEnvelope struct {
	Purpose string `json:"purpose"`
	Scope   string `json:"owner_scope"`
	Payload []byte `json:"payload"`
}

// ProtectExecution 只返回当前用户加密后的字节，不写临时明文文件，也不降级存储。
func ProtectExecution(scope string, payload []byte) ([]byte, error) {
	if scope == "" || len(scope) > 512 || len(payload) == 0 || len(payload) > 1<<20 {
		return nil, errors.New("执行请求缺少作用域或大小不正确")
	}
	raw, err := json.Marshal(executionEnvelope{Purpose: "HRPlus/execution/v1", Scope: scope, Payload: payload})
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	return protect(raw)
}

// UnprotectExecution 解密后核对用途与原账号，跨环境或跨账号密文不能用于补传。
func UnprotectExecution(scope string, cipher []byte) ([]byte, error) {
	if scope == "" || len(cipher) == 0 || len(cipher) > 4<<20 {
		return nil, errors.New("受保护执行请求格式不正确")
	}
	raw, err := unprotect(cipher)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var envelope executionEnvelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, errors.New("受保护执行请求内容损坏")
	}
	if envelope.Purpose != "HRPlus/execution/v1" || envelope.Scope != scope || len(envelope.Payload) == 0 || len(envelope.Payload) > 1<<20 {
		clear(envelope.Payload)
		return nil, errors.New("执行请求与当前账号作用域不一致")
	}
	return envelope.Payload, nil
}
