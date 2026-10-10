// 本文件提供 HRPlus 实际程序重启后的招聘执行验收，云端和网站均隔离，不连接业务服务或真实候选人。
package positionrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"net/http"
	"strings"
	"testing"
	"time"
)

// nativeRestartReportGate 保存原报告补传台账，先拒绝回执，再由测试在进程退出后恢复网络。
type nativeRestartReportGate struct {
	Ready     bool
	Summaries [][]byte
}

// TestNativeAgentScheduledExecutionRestart 验证受保护登录冷恢复后实际后台执行计划并保存浏览器确认事实。
func TestNativeAgentScheduledExecutionRestart(t *testing.T) {
	runNativeReceiptRestart(t, true, true, true)
}

// nativeRestartExecutionCloud 仅返回虚构配置和原许可回执；实际动作由原生程序、Worker 与浏览器完成。
// 调用方持有共同测试锁，避免云端回执与观察结果并发改变。
func nativeRestartExecutionCloud(t *testing.T, w http.ResponseWriter, r *http.Request, permit *planmodel.Permit, replies map[string][]byte, reports *nativeRestartReportGate) bool {
	t.Helper()
	var result any
	switch r.URL.Path {
	case "/api/subscription/status":
		result = map[string]any{"subscription": map[string]any{"active": true, "allow_auto_reply": true}}
	case "/api/config/user-preferences":
		result = map[string]any{"config": map[string]any{}}
	case "/api/config/effective-ai":
		result = map[string]any{"config": map[string]any{}}
	case "/api/positions/native-java":
		result = map[string]any{"position": map[string]any{"id": "native-java", "name": "Java", "platform_id": "boss", "match_limit": 1, "keywords": []string{}, "common_config": map[string]any{"position_name": "Java", "mode_default": "keyword", "detail_mode": "keyword"}}}
	case "/api/platforms/config/":
		body, _ := json.Marshal(map[string]any{"id": "boss", "auth": map[string]any{"pages": []any{map[string]any{"url": "https://www.zhipin.com/web/chat/recommend", "entry": true}}}, "position": map[string]any{"current": map[string]any{"target_classes": []string{"fixture-job"}, "parent_classes": []string{}}, "switchBtn": map[string]any{"target_classes": []string{"fixture-picker"}, "parent_classes": []string{}}, "list": map[string]any{"target_classes": []string{"fixture-menu"}, "parent_classes": []string{}}, "item": map[string]any{"target_classes": []string{"fixture-item"}, "parent_classes": []string{}}, "itemText": map[string]any{"target_classes": []string{"fixture-title"}, "parent_classes": []string{}}}, "card": map[string]any{"item": map[string]any{"selector": ".candidate-card-wrap"}, "fields": map[string]any{"name": map[string]any{"selector": ".candidate-name"}}}, "actions": map[string]any{"greetBtn": map[string]any{"selector": ".greet-btn"}, "continueBtn": map[string]any{"selector": ".continue-btn"}}})
		result = map[string]any{"configs": []any{map[string]any{"config_key": "platform.boss", "config_value": string(body)}}}
	default:
		if strings.HasPrefix(r.URL.Path, "/api/positions/native-java/") {
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
	if r.URL.Path == "/api/execution-plan-runs/"+permit.Run.ID+"/items/"+permit.Run.Items[0].ID+"/prepare" {
		var input cloudapi.PlanItemTaskRequest
		if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
			t.Error(e)
		}
		input.RunID, input.ItemRunID = permit.Run.ID, permit.Run.Items[0].ID
		if prior := replies[input.RequestID]; prior != nil {
			_, _ = w.Write(prior)
			return true
		}
		if input.RunID != permit.Run.ID || input.OwnerID != permit.Owner.OwnerID || input.MachineID != permit.Owner.MachineID {
			t.Error("重启准备任务来源不匹配")
		}
		if permit.Run.Items[0].TaskRunID == "" {
			permit.Run.Sequence++
			permit.Run.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000099"
		}
		replies[input.RequestID], _ = json.Marshal(map[string]any{"ok": true, "permit": permit})
		_, _ = w.Write(replies[input.RequestID])
		return true
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
			permit.Run.FinishedAt = &now
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
