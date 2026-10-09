// 本文件使用真实 Windows DPAPI 验证 HRPlus 会话加密、重开恢复、损坏拒绝与日志脱敏。
package protectedsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsProtectedSession 验证磁盘和普通输出没有令牌，存储对象重建后仍能由当前用户解密。
func TestWindowsProtectedSession(t *testing.T) {
	directory := t.TempDir()
	s := New(directory)
	value := Session{CloudBase: "http://127.0.0.1:8084", UserEmail: "fixture@example.com", TenantID: "fixture-team", MachineID: "fixture-machine", Token: "fixture-secret-do-not-log"}
	if err := s.Save(value); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "protected-session.bin"))
	if err != nil || bytes.Contains(raw, []byte(value.Token)) {
		t.Fatal("磁盘保存了明文令牌", err)
	}
	loaded, err := New(directory).Load()
	if err != nil || loaded.Token != value.Token || loaded.UserEmail != value.UserEmail {
		t.Fatal("DPAPI 重开恢复失败", err)
	}
	encoded, _ := json.Marshal(value)
	for _, output := range []string{string(encoded), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
		if strings.Contains(output, value.Token) {
			t.Fatal("普通输出泄露令牌")
		}
	}
	raw[len(raw)-1] ^= 255
	if err = os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(); err == nil {
		t.Fatal("损坏密文仍恢复成功")
	}
	if err = s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(); !os.IsNotExist(err) {
		t.Fatal("退出后仍保留会话", err)
	}
}
