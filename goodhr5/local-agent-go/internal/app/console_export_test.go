// 本文件用真实静态导出产物验证 HRPlus 本地 HTTP 控制台的计划路由、脚本和 RSC 文件，不运行招聘任务。
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestExecutionPlanStaticConsole 检查真实导出页面按原字节提供，资源没有被首页兜底掩盖。
func TestExecutionPlanStaticConsole(t *testing.T) {
	root := os.Getenv("HRPLUS_M2_CONSOLE_OUT")
	if root == "" {
		t.Skip("需要明确的 M2 静态控制台产物目录")
	}
	var manifest struct {
		Schema int    `json:"schema_version"`
		Cloud  string `json:"cloud_api_base"`
		Hash   string `json:"execution_plans_sha256"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "hrplus-console-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Cloud == "" {
		t.Fatal("缺少原静态构建记录")
	}
	expected, err := os.ReadFile(filepath.Join(root, "admin", "execution-plans.html"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(expected)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.Hash) {
		t.Fatal("计划页与原构建摘要不一致")
	}
	server := newCapabilityTestServer(t)
	server.cfg.Environment = "prod"
	server.cfg.FrontendDir = root
	read := func(path string) []byte {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleConsole(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatal("静态资源读取失败", path, response.Code)
		}
		return response.Body.Bytes()
	}
	if actual := read("/admin/execution-plans"); !bytes.Equal(actual, expected) {
		t.Fatal("计划路由返回首页或另一版本")
	}
	refs := regexp.MustCompile(`<script[^>]+src="([^"]+)"`).FindAllSubmatch(expected, -1)
	if len(refs) == 0 {
		t.Fatal("导出页没有脚本")
	}
	foundCloud := false
	for _, ref := range refs {
		path := string(ref[1])
		if !strings.HasPrefix(path, "/_next/") {
			continue
		}
		body := read(path)
		if len(body) == 0 || bytes.HasPrefix(body, []byte("<!DOCTYPE")) || bytes.HasPrefix(body, []byte("<!doctype")) {
			t.Fatal("脚本被首页兜底", path)
		}
		if bytes.Contains(body, []byte(manifest.Cloud)) {
			foundCloud = true
		}
	}
	if !foundCloud {
		t.Fatal("脚本未包含原构建后端地址")
	}
	rscPath := filepath.Join("admin", "execution-plans", "__next._tree.txt")
	rsc, err := os.ReadFile(filepath.Join(root, rscPath))
	if err != nil {
		t.Fatal(err)
	}
	if actual := read("/" + filepath.ToSlash(rscPath)); !bytes.Equal(actual, rsc) {
		t.Fatal("RSC 文件被首页兜底")
	}
}
