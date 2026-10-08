// 本文件验证 HRPlus 原始收据补传、丢回执重试、登录失效等待和冲突隔离，不调用浏览器。
package regreetupload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/config"
	"goodhr5/local-agent-go/internal/localdb"
)

// queuedReceipt 创建真实 SQLite 的已确认发送与待补传结果。
func queuedReceipt(t *testing.T, base string) (*localdb.DB, string, cloudapi.ReGreetReceiptRequest) {
	t.Helper()
	db, err := localdb.Open(&config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	record, err := db.PrepareAutoReply(t.Context(), localdb.AutoReplyRecord{ProfileScope: "account-scope", Platform: "boss", ConversationID: "conversation", InboundFingerprint: "basis", PositionID: "position", RunID: "run", ContextFingerprint: "context", ReplyFingerprint: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionAutoReply(t.Context(), record.ID, "prepared", "sending", ""); err != nil {
		t.Fatal(err)
	}
	request := cloudapi.ReGreetReceiptRequest{MachineID: "machine", OperationID: record.ID, Platform: "boss", PlatformCandidateID: "candidate", Success: true, RunID: "run", BaseContactAt: time.Now().Add(-time.Hour), SentAt: time.Now(), MessageText: "复打消息"}
	body := struct {
		PositionID string `json:"position_id"`
		cloudapi.ReGreetReceiptRequest
	}{"position", request}
	raw, _ := json.Marshal(body)
	if err = db.ConfirmReGreetAndQueue(t.Context(), localdb.ReGreetOutbox{OperationID: record.ID, ProfileScope: record.ProfileScope, Platform: record.Platform, CandidateID: "candidate", APIBase: base, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	return db, record.ProfileScope, request
}

// TestLegacyReceiptOwnerCheck 验证旧作用域收据须核对原任务所有者；换回原账号仍补传原编号，不丢原事实。
func TestLegacyReceiptOwnerCheck(t *testing.T) {
	owner := "other@example.com"
	posts := 0
	var received cloudapi.ReGreetReceiptRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"email": owner}})
		case "/api/task-runs/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "run", "user_email": "original@example.com", "position_id": "position", "platform_id": "boss", "machine_id": "machine"}})
		case "/api/positions/position/re-greet-report":
			posts++
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: received.OperationID, ResultCount: received.BaseCount + 1, SentAt: received.SentAt, ReceivedAt: time.Now()}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	db, scope, original := queuedReceipt(t, server.URL)
	u := New(db)
	if count, err := u.FlushLegacyOwned(t.Context(), scope, "fixture-token", cloudapi.SessionOwnerScope(server.URL, owner), server.URL); err != nil || count != 0 || posts != 0 {
		t.Fatalf("其他账号补传了旧收据 count=%d posts=%d err=%v", count, posts, err)
	}
	if pending, err := db.PendingReGreetForCandidate(t.Context(), scope, "boss", "candidate"); err != nil || !pending {
		t.Fatal("未核对旧事实被丢弃")
	}
	owner = "original@example.com"
	if err := db.ResumeReGreetUploads(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if count, err := u.FlushLegacyOwned(t.Context(), scope, "fixture-token", cloudapi.SessionOwnerScope(server.URL, owner), server.URL); err != nil || count != 1 || posts != 1 {
		t.Fatalf("原账号未恢复旧收据 count=%d posts=%d err=%v", count, posts, err)
	}
	if received.OperationID != original.OperationID || received.MessageText != original.MessageText || !received.SentAt.Equal(original.SentAt) || received.RunID != original.RunID || received.MachineID != original.MachineID {
		t.Fatal("补传改变了原编号或发送事实")
	}
}

// TestUploadLostReceipt 验证服务器已保存但丢回执后补传原编号，未产生新的发送意图。
func TestUploadLostReceipt(t *testing.T) {
	calls := 0
	operations := map[string]bool{}
	var first cloudapi.ReGreetReceiptRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request cloudapi.ReGreetReceiptRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if calls == 0 {
			first = request
		} else if first.OperationID != request.OperationID || first.MessageText != request.MessageText || !first.SentAt.Equal(request.SentAt) {
			t.Error("补传修改了发送事实")
		}
		operations[request.OperationID] = true
		calls++
		if calls == 1 {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "receipt response lost"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: request.OperationID, ResultCount: request.BaseCount + 1, SentAt: request.SentAt, ReceivedAt: time.Now()}})
	}))
	defer server.Close()
	db, scope, _ := queuedReceipt(t, server.URL)
	u := New(db)
	now := time.Now().Add(time.Second)
	u.Now = func() time.Time { return now }
	if count, err := u.Flush(t.Context(), scope, "in-memory-token"); err != nil || count != 0 {
		t.Fatalf("首次不应确认 %d %v", count, err)
	}
	if count, err := u.Flush(t.Context(), scope, "in-memory-token"); err != nil || count != 0 || calls != 1 {
		t.Fatalf("退避前重复上报 %d %v", count, err)
	}
	now = now.Add(5 * time.Second)
	if count, err := u.Flush(t.Context(), scope, "in-memory-token"); err != nil || count != 1 || len(operations) != 1 {
		t.Fatalf("未确认原收据 %d %v", count, err)
	}
}

// TestUploadAuthAndConflict 验证登录失效等待重新登录，事实冲突永不自动重发或反复补传。
func TestUploadAuthAndConflict(t *testing.T) {
	for _, status := range []int{401, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "test"})
			}))
			defer server.Close()
			db, scope, _ := queuedReceipt(t, server.URL)
			u := New(db)
			if _, err := u.Flush(t.Context(), scope, "token"); err != nil {
				t.Fatal(err)
			}
			u.Now = func() time.Time { return time.Now().Add(time.Hour) }
			if _, err := u.Flush(t.Context(), scope, "token"); err != nil || calls != 1 {
				t.Fatalf("等待状态仍重试 %v calls=%d", err, calls)
			}
			if pending, err := db.PendingReGreetForCandidate(t.Context(), scope, "boss", "candidate"); err != nil || !pending {
				t.Fatal("未确认结果未阻止下一次发送")
			}
		})
	}
}
