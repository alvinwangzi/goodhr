// 本文件验证环境配置缺失时拒绝启动，防止本地程序静默连接错误环境。
package config

import (
	"testing"
)

// TestNewWithDataDirRejectsMissingEndpoints 验证未配置环境地址时不能退回写死的远程地址。
func TestNewWithDataDirRejectsMissingEndpoints(t *testing.T) {
	for _, key := range []string{"GOODHR_APP_ENV", "GOODHR_CLOUD_API_BASE", "GOODHR_CONSOLE_URL", "GOODHR_CONSOLE_MANIFEST_URL"} {
		t.Setenv(key, "")
	}
	if _, err := NewWithDataDir("127.0.0.1", 55271, t.TempDir()); err == nil {
		t.Fatal("缺少环境和地址配置时应拒绝启动，不能静默使用远程默认值")
	}
}
