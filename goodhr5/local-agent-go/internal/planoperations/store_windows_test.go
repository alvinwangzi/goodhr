// 本文件使用真实 Windows DPAPI、SQLite 重开及 HTTP 夹具验证 HRPlus 原请求和安全补传。
package planoperations

import (
	"bytes"
	"database/sql"
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

// TestProtectedPrepareRestart 验证执行项原准备请求加密、真实重开恢复、换项冲突及后台不自动准备。
func TestProtectedPrepareRestart(t *testing.T) {
	db, cfg, p, claim := operationFixture(t)
	store := New(db)
	input := cloudapi.PlanItemTaskRequest{RunID: claim.RunID, ItemRunID: p.Run.Items[0].ID, RequestID: "60000000-0000-0000-0000-000000000004", OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential}
	record, err := store.StagePrepare(t.Context(), "A", claim.PlanID, input)
	if err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.ItemRunID = p.Run.Items[1].ID
	if _, err = store.StagePrepare(t.Context(), "A", claim.PlanID, changed); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("同一准备编号可以换执行项", err)
	}
	for _, path := range []string{db.Path(), db.Path() + "-wal"} {
		raw, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(input.Credential)) {
			t.Fatal("执行项凭证写入明文")
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
	original, err := restored.OriginalPrepare(t.Context(), "A", input.RequestID)
	if err != nil || !reflect.DeepEqual(original, input) {
		t.Fatal("重开改变原执行项请求", err)
	}
	retry, err := restored.StagePrepare(t.Context(), "A", claim.PlanID, input)
	if err != nil || retry.Sequence != record.Sequence || !bytes.Equal(retry.Cipher, record.Cipher) {
		t.Fatal("重复准备改变原密文或顺序", err)
	}
	if _, err = reopened.NextPlanUpdate(t.Context(), "A"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("原准备请求被后台选中", err)
	}
	if _, err = restored.OriginalPrepare(t.Context(), "B", input.RequestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("其他账号读取原准备请求", err)
	}
}

// TestProtectedDispatchRestart 验证轮换随原请求加密保存，重开后补传同时确认进度和轮换。
func TestProtectedDispatchRestart(t *testing.T) {
	db, cfg, permit, claim := operationFixture(t)
	next := permit.Run
	next.Sequence++
	next.State = "running"
	input := cloudapi.PlanRunUpdateRequest{RunID: next.ID, Action: "status", RequestID: "60000000-0000-0000-0000-000000000008", OwnerID: claim.OwnerID, MachineID: claim.MachineID, Credential: claim.Credential, Sequence: next.Sequence, State: next.State}
	for _, item := range next.Items {
		input.Items = append(input.Items, planmodel.ItemUpdate{ID: item.ID, ItemID: item.ItemID, State: item.State, Actions: item.Actions})
	}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"email": "fixture@example.com"}})
			return
		}
		if r.URL.Path != "/api/execution-plan-runs/"+next.ID+"/status" {
			t.Error("补传路径不匹配", r.URL.Path)
		}
		var actual cloudapi.PlanRunUpdateRequest
		if json.NewDecoder(r.Body).Decode(&actual) != nil || actual.RequestID != input.RequestID || actual.Sequence != input.Sequence {
			t.Error("补传改变原编号或序号")
		}
		posts.Add(1)
		result := permit
		result.Run = next
		result.Owner.State = "running"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "permit": result})
	}))
	defer server.Close()
	scope := cloudapi.SessionOwnerScope(server.URL, "fixture@example.com")
	if err := db.SavePlanRunSnapshot(t.Context(), scope, permit.Run); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": scope, "Run": next, "PagesSinceScan": 2})
	store := New(db)
	if _, err := store.StageUpdate(t.Context(), scope, next.PlanID, input, raw); err != nil {
		t.Fatal(err)
	}
	changed, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": scope, "Run": next, "PagesSinceScan": 3})
	if _, err := store.StageUpdate(t.Context(), scope, next.PlanID, input, changed); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("原编号可替换轮换", err)
	}
	wrong, _ := json.Marshal(map[string]any{"Schema": 1, "OwnerScope": "other", "Run": next})
	if _, err := store.StageUpdate(t.Context(), scope, next.PlanID, input, wrong); !errors.Is(err, localdb.ErrPlanRequestConflict) {
		t.Fatal("错误账号轮换可登记", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := localdb.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := New(reopened)
	original, err := restored.OriginalMessageDispatch(t.Context(), scope, input.RequestID)
	if err != nil || !bytes.Equal(original, raw) {
		t.Fatal("重开改变原轮换", err)
	}
	if sent, err := restored.UploadNext(t.Context(), cloudapi.New(server.URL), Authority{Token: "fixture-token", OwnerScope: scope, StillCurrent: func() bool { return true }}); err != nil || !sent {
		t.Fatal("原状态补传失败", err)
	}
	current, err := reopened.PlanRunSnapshot(t.Context(), scope, next.ID)
	if err != nil || current.Sequence != next.Sequence {
		t.Fatal("补传未确认规范进度", err)
	}
	dispatch, err := reopened.PlanMessageDispatch(t.Context(), scope, next.ID)
	var saved struct {
		PagesSinceScan int
		Run            planmodel.Run
	}
	if err != nil || json.Unmarshal(dispatch, &saved) != nil || saved.PagesSinceScan != 2 || saved.Run.Sequence != next.Sequence {
		t.Fatal("补传丢失原轮换", err)
	}
	op, err := reopened.PlanOperation(t.Context(), scope, input.RequestID)
	if err != nil || op.State != "confirmed" || posts.Load() != 1 {
		t.Fatal("回执没有共同确认", err)
	}
}

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
