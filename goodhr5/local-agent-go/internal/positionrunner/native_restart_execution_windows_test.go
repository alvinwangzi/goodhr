// 本文件提供 HRPlus 实际程序重启后的招聘执行验收，云端和网站均隔离，不连接业务服务或真实候选人。
package positionrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// nativeRestartReportGate 保存原报告补传台账，先拒绝回执，再由测试在进程退出后恢复网络。
type nativeRestartReportGate struct {
	ReGreetReceipts map[string]struct {
		Body     string
		Response []byte
	}
	ContactAt       time.Time
	ReGreetReported bool
	WindowRestDone  bool
	ScanStarted     bool
	Ready           bool
	Summaries       [][]byte
}

// TestNativeAgentScheduledExecutionRestart 验证受保护登录冷恢复后实际后台执行计划并保存浏览器确认事实。
func TestNativeAgentScheduledExecutionRestart(t *testing.T) {
	runNativeReceiptRestart(t, true, true, true)
}

// nativeRestartExecutionCloud 仅返回虚构配置和原许可回执；实际动作由原生程序、Worker 与浏览器完成。
// 调用方持有共同测试锁，避免云端回执与观察结果并发改变。
func nativeRestartExecutionCloud(t *testing.T, w http.ResponseWriter, r *http.Request, permit *planmodel.Permit, replies map[string][]byte, reports *nativeRestartReportGate) bool {
	t.Helper()
	if strings.HasPrefix(r.URL.Path, "/model/") {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "should_send") {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"should_send":true,"message":"方便时可以继续了解岗位。","is_refused":false,"refuse_reason":""}`}}}})
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"action":"reply","text":"岗位仍在招聘，欢迎沟通。","reason":"虚构岗位问答","request_resume":false}`}}}})
		return true
	}
	var result any
	switch r.URL.Path {
	case "/api/subscription/status":
		result = map[string]any{"subscription": map[string]any{"active": true, "allow_auto_reply": true}}
	case "/api/config/user-preferences":
		result = map[string]any{"config": map[string]any{}}
		if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "timed-compound" {
			// 使用真实休息跨过既有60秒检查点，不加速或替换安装程序时钟。
			result = map[string]any{"config": map[string]any{"rest_after_candidates_min": 1, "rest_after_candidates_max": 1, "rest_times_min": 1, "rest_times_max": 1, "rest_duration_min": 1.5, "rest_duration_max": 1.5}}
		}
		if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "across-windows" && !reports.WindowRestDone {
			result = map[string]any{"config": map[string]any{"rest_after_candidates_min": 1, "rest_after_candidates_max": 1, "rest_times_min": 1, "rest_times_max": 1, "rest_duration_min": 20, "rest_duration_max": 20}}
		}
	case "/api/config/effective-ai":
		result = map[string]any{"config": map[string]any{"base_url": "http://" + r.Host + "/model", "api_key": "fixture-token", "model": "fixture"}}
	case "/api/positions/native-java":
		result = map[string]any{"position": map[string]any{"id": "native-java", "name": "Java", "platform_id": "boss", "match_limit": 1, "keywords": []string{}, "common_config": map[string]any{"position_name": "Java", "mode_default": "keyword", "detail_mode": "keyword"}}}
		if scenario := os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO"); scenario == "unknown-platform" || scenario == "unsupported-messages" {
			platform := "fixture-unknown-platform"
			if scenario == "unsupported-messages" {
				platform = "zhaopin"
			}
			result = map[string]any{"position": map[string]any{"id": "native-java", "name": "Java", "platform_id": platform, "match_limit": 1}}
		}
		if nativeCompoundScenario() {
			result = map[string]any{"position": map[string]any{"id": "native-java", "name": "Go", "platform_id": "boss", "match_limit": 3, "keywords": []string{}, "common_config": map[string]any{"position_name": "Go", "mode_default": "keyword", "detail_mode": "keyword"}}}
		}
	case "/api/positions/native-java/screenings/find":
		if nativeCompoundScenario() {
			result = map[string]any{"item": map[string]any{"id": "fixture-screen-A", "position_id": "native-java", "platform": "boss", "platform_candidate_id": "opaque-A", "candidate_name": "同名候选人 A", "score": 90}}
		} else {
			result = map[string]any{"ok": true}
		}
	case "/api/positions/native-java/re-greet-candidates":
		items := []any{}
		if reports.ContactAt.IsZero() {
			reports.ContactAt = time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Microsecond)
		}
		if nativeCompoundScenario() && !reports.ReGreetReported && (os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") != "timed-compound" || reports.ScanStarted) {
			items = append(items, map[string]any{"id": "fixture-screen-B", "position_id": "native-java", "platform": "boss", "platform_candidate_id": "opaque-B", "candidate_name": "同名候选人 B", "greeted_at": reports.ContactAt.Format(time.RFC3339Nano), "re_greet_count": 0})
		}
		result = map[string]any{"ok": true, "re_greet_receipts": true, "items": items}
	case "/api/positions/native-java/re-greet-report":
		var input cloudapi.ReGreetReceiptRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		if len(permit.Run.Items) == 0 || input.RunID != permit.Run.Items[0].TaskRunID || input.PlatformCandidateID != "opaque-B" || !input.Success || input.MachineID != permit.Owner.MachineID || input.Platform != "boss" || input.OperationID == "" || input.BaseCount != 0 || !input.BaseContactAt.Equal(reports.ContactAt) || input.SentAt.IsZero() {
			t.Error("复打收据未绑定原任务及真实映射")
			w.WriteHeader(http.StatusConflict)
			return true
		}
		body, _ := json.Marshal(input)
		if prior, ok := reports.ReGreetReceipts[input.OperationID]; ok {
			if prior.Body != string(body) {
				t.Error("同一复打操作改变原发送事实")
				w.WriteHeader(http.StatusConflict)
				return true
			}
			_, _ = w.Write(prior.Response)
			return true
		}
		reports.ReGreetReported = true
		result = map[string]any{"receipt": cloudapi.ReGreetReceiptResponse{OperationID: input.OperationID, ResultCount: input.BaseCount + 1, SentAt: input.SentAt, ReceivedAt: time.Now()}}
		if reports.ReGreetReceipts == nil {
			reports.ReGreetReceipts = map[string]struct {
				Body     string
				Response []byte
			}{}
		}
		response, _ := json.Marshal(result)
		reports.ReGreetReceipts[input.OperationID] = struct {
			Body     string
			Response []byte
		}{string(body), response}
	case "/api/positions/native-sales":
		result = map[string]any{"position": map[string]any{"id": "native-sales", "name": "销售", "platform_id": "boss", "match_limit": 1, "keywords": []string{}, "common_config": map[string]any{"position_name": "销售", "mode_default": "keyword", "detail_mode": "keyword"}}}
	case "/api/platforms/config/":
		if nativeCompoundScenario() {
			result = map[string]any{"configs": []any{map[string]any{"config_key": "platform.boss", "config_value": `{"id":"boss","auth":{"pages":[{"url":"https://www.zhipin.com/web/chat/recommend","entry":true}]},"position":{"current":{"selector":".current-position"},"switchBtn":{"selector":".switch-position"},"list":{"selector":".position-list"},"item":{"selector":".position-item"},"itemText":{"selector":".position-name"}},"card":{"item":{"selector":".candidate-card-wrap"},"fields":{"name":{"selector":".candidate-name"}}},"actions":{"greetBtn":{"selector":".greet-btn"},"continueBtn":{"selector":".continue-btn"}}}`}}}
			break
		}
		body, _ := json.Marshal(map[string]any{"id": "boss", "auth": map[string]any{"pages": []any{map[string]any{"url": "https://www.zhipin.com/web/chat/recommend", "entry": true}}}, "position": map[string]any{"current": map[string]any{"target_classes": []string{"fixture-job"}, "parent_classes": []string{}}, "switchBtn": map[string]any{"target_classes": []string{"fixture-picker"}, "parent_classes": []string{}}, "list": map[string]any{"target_classes": []string{"fixture-menu"}, "parent_classes": []string{}}, "item": map[string]any{"target_classes": []string{"fixture-item"}, "parent_classes": []string{}}, "itemText": map[string]any{"target_classes": []string{"fixture-title"}, "parent_classes": []string{}}}, "card": map[string]any{"item": map[string]any{"selector": ".candidate-card-wrap"}, "fields": map[string]any{"name": map[string]any{"selector": ".candidate-name"}}}, "actions": map[string]any{"greetBtn": map[string]any{"selector": ".greet-btn"}, "continueBtn": map[string]any{"selector": ".continue-btn"}}})
		result = map[string]any{"configs": []any{map[string]any{"config_key": "platform.boss", "config_value": string(body)}}}
	default:
		if strings.HasPrefix(r.URL.Path, "/api/positions/native-java/") || strings.HasPrefix(r.URL.Path, "/api/positions/native-sales/") {
			result = map[string]any{"ok": true}
		}
	}
	if result != nil {
		_ = json.NewEncoder(w).Encode(result)
		return true
	}
	if permit.Run.ID == "" || !strings.HasPrefix(r.URL.Path, "/api/execution-plan-runs/"+permit.Run.ID) {
		return false
	}
	if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/report" {
		var input struct {
			Summary   planmodel.Report `json:"summary"`
			SyncState string           `json:"sync_state"`
			MachineID string           `json:"machine_id"`
		}
		if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
			t.Error(e)
		}
		if input.Summary.RunID != permit.Run.ID || input.MachineID != permit.Owner.MachineID {
			t.Error("补传报告没有原运行或电脑")
		}
		raw, _ := json.Marshal(input.Summary)
		reports.Summaries = append(reports.Summaries, raw)
		if !reports.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "受控报告网络失败"})
			return true
		}
		hash := sha256.Sum256(raw)
		now := time.Now().UTC()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "report": cloudapi.PlanReportReceipt{RunID: permit.Run.ID, BodyHash: hex.EncodeToString(hash[:]), Summary: input.Summary, SyncState: input.SyncState, NotificationState: "not_configured", CreatedAt: now, UpdatedAt: now}})
		return true
	}
	for index := range permit.Run.Items {
		if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/items/"+permit.Run.Items[index].ID+"/prepare" {
			var input cloudapi.PlanItemTaskRequest
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
			}
			input.RunID, input.ItemRunID = permit.Run.ID, permit.Run.Items[index].ID
			if prior := replies[input.RequestID]; prior != nil {
				_, _ = w.Write(prior)
				return true
			}
			if input.RunID != permit.Run.ID || input.OwnerID != permit.Owner.OwnerID || input.MachineID != permit.Owner.MachineID {
				t.Error("重启准备任务来源不匹配")
			}
			if permit.Run.Items[index].TaskRunID == "" {
				permit.Run.Sequence++
				permit.Run.Items[index].TaskRunID = fmt.Sprintf("70000000-0000-0000-0000-%012d", index+99)
				if os.Getenv("HRPLUS_M2_ACCEPTANCE_SCENARIO") == "stop-edit" {
					permit.Run.Items[index].TaskRunID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(permit.Run.Items[index].ID)).String()
				}
			}
			replies[input.RequestID], _ = json.Marshal(map[string]any{"ok": true, "permit": permit})
			_, _ = w.Write(replies[input.RequestID])
			return true
		}
	}
	if strings.HasSuffix(r.URL.Path, "/status") || strings.HasSuffix(r.URL.Path, "/release") {
		var input cloudapi.PlanRunUpdateRequest
		if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
			t.Error(e)
		}
		input.RunID = permit.Run.ID
		input.Action = strings.TrimPrefix(r.URL.Path, "/api/execution-plan-runs/"+permit.Run.ID+"/")
		if prior := replies[input.RequestID]; prior != nil {
			_, _ = w.Write(prior)
			return true
		}
		if input.RunID != permit.Run.ID || input.OwnerID != permit.Owner.OwnerID || input.Sequence != permit.Run.Sequence+1 {
			t.Error("重启进度没有沿原运行单调保存")
		}
		permit.Run.Sequence, permit.Run.State, permit.Run.CurrentItem = input.Sequence, input.State, input.CurrentItem
		permit.Run.EndReason = input.EndReason
		for index, item := range input.Items {
			permit.Run.Items[index].State, permit.Run.Items[index].Actions = item.State, item.Actions
			if input.Action == "release" && input.State != "completed" && input.State != "waiting_window" {
				if item.State == "pending" || item.State == "running" {
					permit.Run.Items[index].State = "stopped"
				}
				for action, progress := range permit.Run.Items[index].Actions {
					if progress.State == "active" {
						progress.State = "stopped"
						permit.Run.Items[index].Actions[action] = progress
					}
				}
			}
		}
		if input.State == "running" && permit.Run.StartedAt == nil {
			now := time.Now().UTC()
			permit.Run.StartedAt = &now
		}
		permit.Owner.State = "running"
		if input.State == "draining" {
			permit.Owner.State = "releasing"
		}
		if input.Action == "release" {
			now := time.Now().UTC()
			if input.State == "waiting_window" {
				permit.Run.FinishedAt = nil
				reports.WindowRestDone = true
			} else {
				permit.Run.FinishedAt = &now
			}
			permit.Owner.State = "released"
		}
		replies[input.RequestID], _ = json.Marshal(map[string]any{"ok": true, "permit": permit})
		_, _ = w.Write(replies[input.RequestID])
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "run": permit.Run})
		return true
	}
	// 报告和日志刻意保持离线待补传，不能用泛成功回执伪造云端确认。
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "受控报告与日志暂时离线"})
	return true
}
