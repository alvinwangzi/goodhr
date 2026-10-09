// 本文件使用真实 Windows DPAPI、SQLite 重开及 HTTP 夹具验证 HRPlus 原请求和安全补传。
package planoperations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// operationFixture 为当前用户创建独立测试数据库和不含登录凭证的原运行夹具。
func operationFixture(t *testing.T) (*localdb.DB, *config.Config, planmodel.Permit, cloudapi.PlanClaimRequest) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir()}
	db, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p planmodel.Permit
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	claim := cloudapi.PlanClaimRequest{PlanID: p.Run.PlanID, ActivationID: p.Run.ActivationID, ExpectedVersion: 1, ExecutionDate: p.Run.ExecutionDate, RunID: p.Run.ID, RequestID: "60000000-0000-0000-0000-000000000001", MachineID: p.Owner.MachineID, OwnerID: p.Owner.OwnerID, Credential: strings.Repeat("fixture-execution-secret-", 3), LocalReserved: true}
	return db, cfg, p, claim
}

// TestProtectedPlanRequestRestart 验证原请求密文落盘，实际重开后恢复编号与凭证而不重新生成。
func TestProtectedPlanRequestRestart(t *testing.T) {
	db, cfg, p, claim := operationFixture(t)
	store := New(db)
	original, err := store.StageClaim(t.Context(), "A", claim)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.StageClaim(t.Context(), "A", claim)
	if err != nil || retry.Sequence != original.Sequence || !bytes.Equal(retry.Cipher, original.Cipher) {
		t.Fatal("重试替换原请求", err)
	}
	changed := claim
	changed.ExpectedVersion = 2
	if _, err = store.StageClaim(t.Context(), "A", changed); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("原编号换版本", err)
	}
	update := cloudapi.PlanRunUpdateRequest{RunID: claim.RunID, Action: "release", RequestID: "60000000-0000-0000-0000-000000000002", OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: 2, State: "waiting_window", CleanupConfirmed: true}
	queued, err := store.StageUpdate(t.Context(), "A", p.Run.PlanID, update)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{db.Path(), db.Path() + "-wal"} {
		raw, e := os.ReadFile(path)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte(claim.Credential)) {
			t.Fatal("SQLite 或 WAL 写入凭证明文")
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := New(reopened)
	loaded, err := restored.OriginalClaim(t.Context(), "A", claim.RequestID)
	if err != nil || !reflect.DeepEqual(loaded, claim) {
		t.Fatal("重启改变原领取事实", err)
	}
	loadedUpdate, err := restored.OriginalUpdate(t.Context(), "A", update.RequestID)
	if err != nil || !reflect.DeepEqual(loadedUpdate, update) {
		t.Fatal("重启改变原释放事实", err)
	}
	next, err := reopened.NextPlanUpdate(t.Context(), "A")
	if err != nil || next.RequestID != queued.RequestID || next.Kind != "release" {
		t.Fatal("补传错误选择了领取请求", err)
	}
	swapped := original
	swapped.OwnerScope = "B"
	if _, err = reopened.SavePlanOperation(t.Context(), swapped); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.OriginalClaim(t.Context(), "B", claim.RequestID); err == nil {
		t.Fatal("跨账号密文可解密发送")
	}
}

// TestPlanUploadOriginalReceipt 验证真实所有者核对、授权变更及丢失回执后的原编号重试。
func TestPlanUploadOriginalReceipt(t *testing.T) {
	db, _, p, claim := operationFixture(t)
	store := New(db)
	var posts atomic.Int32
	var mode atomic.Int32
	var current atomic.Bool
	current.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			email := "fixture@example.com"
			if mode.Load() == 1 {
				email = "other@example.com"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": email}})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/"+claim.RunID+"/release" {
			t.Error("补传发送了领取或错误路径", r.URL.Path)
		}
		posts.Add(1)
		var request cloudapi.PlanRunUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.RequestID != "60000000-0000-0000-0000-000000000002" || request.Credential != claim.Credential || !request.CleanupConfirmed {
			t.Error("补传改变原事实")
		}
		if mode.Load() == 2 {
			current.Store(false)
		}
		result := p
		result.Run.Sequence = 2
		result.Run.State = "waiting_window"
		result.Owner.State = "released"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
	}))
	defer server.Close()
	client := cloudapi.New(server.URL)
	scope := cloudapi.SessionOwnerScope(server.URL, "fixture@example.com")
	if _, err := store.StageClaim(t.Context(), scope, claim); err != nil {
		t.Fatal(err)
	}
	authority := Authority{Token: "fixture-token", OwnerScope: scope, StillCurrent: current.Load}
	if strings.Contains(fmt.Sprintf("%#v", authority), authority.Token) {
		t.Fatal("调试日志输出登录令牌")
	}
	if sent, err := store.UploadNext(t.Context(), client, authority); err != nil || sent || posts.Load() != 0 {
		t.Fatal("仅领取请求被自动补传", err)
	}
	update := cloudapi.PlanRunUpdateRequest{RunID: claim.RunID, Action: "release", RequestID: "60000000-0000-0000-0000-000000000002", OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: 2, State: "waiting_window", CleanupConfirmed: true}
	if _, err := store.StageUpdate(t.Context(), scope, claim.PlanID, update); err != nil {
		t.Fatal(err)
	}
	mode.Store(1)
	if sent, err := store.UploadNext(t.Context(), client, authority); err == nil || sent || posts.Load() != 0 {
		t.Fatal("另一账号发送旧请求", err)
	}
	mode.Store(2)
	if sent, err := store.UploadNext(t.Context(), client, authority); err == nil || sent || posts.Load() != 1 {
		t.Fatal("授权变化后确认旧回执", err)
	}
	pending, err := db.PlanOperation(t.Context(), scope, update.RequestID)
	if err != nil || pending.State != "pending" {
		t.Fatal("未核对回执被删除", err)
	}
	mode.Store(0)
	current.Store(true)
	if sent, err := store.UploadNext(t.Context(), client, authority); err != nil || !sent || posts.Load() != 2 {
		t.Fatal("原回执不能重新核对", err)
	}
	confirmed, err := db.PlanOperation(t.Context(), scope, update.RequestID)
	if err != nil || confirmed.State != "confirmed" {
		t.Fatal("明确原回执未确认", err)
	}
	if _, err = store.StageUpdate(t.Context(), scope, claim.PlanID, update); err != nil {
		t.Fatal(err)
	}
	again, err := db.PlanOperation(t.Context(), scope, update.RequestID)
	if err != nil || again.State != "confirmed" {
		t.Fatal("重复登记重开已确认请求", err)
	}
}
