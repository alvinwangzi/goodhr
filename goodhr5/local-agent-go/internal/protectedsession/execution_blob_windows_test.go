// 本文件使用真实 Windows DPAPI 验证 HRPlus 请求密文的用途、账号绑定及损坏拒绝。
package protectedsession

import (
	"bytes"
	"testing"
)

// TestExecutionBlobProtection 验证原请求可恢复，跨账号或会话用途的密文不能混用。
func TestExecutionBlobProtection(t *testing.T) {
	plain := []byte(`{"credential":"fixture-private-execution-secret"}`)
	cipher, err := ProtectExecution("A", plain)
	if err != nil || bytes.Contains(cipher, plain) {
		t.Fatal("执行请求未加密", err)
	}
	restored, err := UnprotectExecution("A", cipher)
	if err != nil || !bytes.Equal(restored, plain) {
		t.Fatal("原密文不可恢复", err)
	}
	clear(restored)
	if _, err = UnprotectExecution("B", cipher); err == nil {
		t.Fatal("密文可跨账号使用")
	}
	cipher[len(cipher)-1] ^= 1
	if _, err = UnprotectExecution("A", cipher); err == nil {
		t.Fatal("损坏密文仍解密成功")
	}
	sessionCipher, err := protect([]byte(`{"credential":"fixture-session-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UnprotectExecution("A", sessionCipher); err == nil {
		t.Fatal("登录会话密文被当成执行请求")
	}
}
