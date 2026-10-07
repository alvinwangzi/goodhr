// 本文件验证页面核对只更新状态摘要，不覆盖简历正文、评分和原始打招呼时间。
package localdb

import (
	"testing"

	"goodhr5/local-agent-go/internal/config"
)

// TestObserveCandidateStatePreservesProfile 验证同名记录按 ID 隔离，已有资料和时间保持原样。
func TestObserveCandidateStatePreservesProfile(t *testing.T) {
	db, err := Open(&config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.CreatePosition(map[string]any{"name": "测试岗位"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"manual", "other"} {
		_, err = db.SaveCandidate(p.ID, map[string]any{"id": id, "candidate_name": "同名", "status": "passed", "raw_text": "完整简历正文", "ai_greet_score": 88.0, "greeted_at": "2026-10-01T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ObserveCandidateState(t.Context(), p.ID, "manual", true, "received"); err != nil {
		t.Fatal(err)
	}
	item, err := db.GetCandidate("manual", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.GetCandidate("other", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item["status"] != "resume_received" || item["raw_text"] != "完整简历正文" || item["greeted_at"] != "2026-10-01T00:00:00Z" || other["status"] != "passed" {
		t.Fatalf("数据被覆盖或同名错绑：%v %v", item, other)
	}
}
