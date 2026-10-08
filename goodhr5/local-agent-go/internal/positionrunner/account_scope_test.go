// 本文件使用真实 SQLite 与独立认证接口验证登录所有者、招聘账号、运行不可变及补传作用域隔离。
package positionrunner

import (
	"context"
	"encoding/json"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
	"net/http"
	"net/http/httptest"
	"testing"
)

// accountProofFixture 仅提供明确的虚构账号证明，不模拟姓名推断。
type accountProofFixture struct {
	id      string
	refresh []bool
}

// ObserveAccountIdentity 记录是否允许首次安全补取，返回当前实际账号标识。
func (f *accountProofFixture) ObserveAccountIdentity(_ context.Context, _ platformcore.Executor, refresh bool) (string, error) {
	f.refresh = append(f.refresh, refresh)
	return f.id, nil
}

// TestRunAccountAndUploaderOwnership 验证账号及云端所有者均进入作用域，并且运行内换账号被拒绝。
func TestRunAccountAndUploaderOwnership(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		owner := "owner-a@example.com"
		if r.Header.Get("Authorization") == "Bearer token-B" {
			owner = "owner-b@example.com"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": owner}})
	}))
	defer cloud.Close()
	db := openRunnerTestDB(t)
	runner := newTestRunner(t, db, &onceWorker{})
	runner.uploadActive = true // 本测试验证领取作用域，补传 HTTP 与故障由 uploader 的独立测试覆盖。
	position := localdb.Position{ID: "p", PlatformID: "boss"}
	create := func() StartOptions {
		cp, err := db.CreateActionRun(t.Context(), localdb.ActionCheckpoint{PositionID: position.ID, Platform: "boss", ProfileScope: "legacy"})
		if err != nil {
			t.Fatal(err)
		}
		return StartOptions{LocalRunID: cp.RunID, Token: "token-A", CloudAPIBase: cloud.URL}
	}
	options := create()
	exec := platformExecutor{runner: runner, positionID: position.ID}
	proof := &accountProofFixture{id: "901"}
	scopeA, bound, err := runner.bindPlatformAccountScope(t.Context(), exec, proof, position, options)
	if err != nil || !bound || scopeA == "legacy" {
		t.Fatalf("绑定失败 %s %v", scopeA, err)
	}
	if _, _, err = runner.bindPlatformAccountScope(t.Context(), exec, proof, position, options); err != nil {
		t.Fatal(err)
	}
	if len(proof.refresh) != 2 || !proof.refresh[0] || proof.refresh[1] {
		t.Fatal("已绑定运行再次刷新页面")
	}
	proof.id = "902"
	if _, _, err = runner.bindPlatformAccountScope(t.Context(), exec, proof, position, options); err == nil {
		t.Fatal("运行内换账号被允许")
	}
	second := create()
	second.Token = "token-B"
	proof.id = "901"
	scopeB, _, err := runner.bindPlatformAccountScope(t.Context(), exec, proof, position, second)
	if err != nil || scopeA == scopeB {
		t.Fatal("不同云端所有者混用同一个平台作用域")
	}
	for owner, wanted := range map[string]string{"owner-a@example.com": scopeA, "owner-b@example.com": scopeB} {
		scopes, err := runner.reGreetUploadScopes(t.Context(), CloudOwnerScope(cloud.URL, owner))
		if err != nil || len(scopes) != 1 || scopes[0] != wanted {
			t.Fatalf("补传混入其他所有者或旧作用域 %v %v", scopes, err)
		}
	}
}

// TestLateUploadSessionProof 验证旧任务和迟到登录证明不覆盖新的已核对会话。
func TestLateUploadSessionProof(t *testing.T) {
	runner := newTestRunner(t, openRunnerTestDB(t), &onceWorker{})
	runner.uploadActive = true
	oldVersion := runner.ReGreetUploadSessionVersion()
	runner.BindReGreetUploadSession("new-token", "new-owner")
	runner.BindVerifiedReGreetUploadSession("old-token", "old-owner", oldVersion)
	runner.mu.Lock()
	token, owner := runner.uploadToken, runner.uploadOwnerScope
	runner.mu.Unlock()
	if token != "new-token" || owner != "new-owner" {
		t.Fatal("旧核对覆盖了新登录")
	}
	currentVersion := runner.ReGreetUploadSessionVersion()
	runner.BindVerifiedReGreetUploadSession("fresh-token", "new-owner", currentVersion)
	runner.mu.Lock()
	token = runner.uploadToken
	runner.mu.Unlock()
	if token != "fresh-token" {
		t.Fatal("当前版本的核对结果未生效")
	}
}
