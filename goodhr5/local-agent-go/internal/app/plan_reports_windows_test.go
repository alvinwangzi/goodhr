// 本文件验证 HRPlus 后台为历史结束运行补生成原报告，不停用周期计划，不访问真实招聘页面。
package app

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/planmodel"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEndedReportSavedOnce 验证上线后补生成历史报告，重复协调不会改变原生成时间或计划状态。
func TestEndedReportSavedOnce(t *testing.T) {
	s, identity, _, _, _, _ := backgroundPlanFixture(t, false)
	if err := s.commitProtectedSession(t.Context(), 0, identity); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var permit planmodel.Permit
	if err := json.Unmarshal(raw, &permit); err != nil {
		t.Fatal(err)
	}
	run := permit.Run
	run.State = "incomplete"
	run.Items[0].State = "stopped"
	run.Items[1].State = "stopped"
	scope := cloudapi.SessionOwnerScope(identity.CloudBase, identity.UserEmail)
	if err := s.db.SavePlanRunSnapshot(t.Context(), scope, run); err != nil {
		t.Fatal(err)
	}
	p := planmodel.Plan{ID: run.PlanID, State: "enabled"}
	current, version := s.currentPlanSession()
	a := s.planAuthority(current, version)
	if err := s.saveEndedPlanReports(t.Context(), []planmodel.Plan{p}, a); err != nil {
		t.Fatal(err)
	}
	first, err := s.db.PlanReportSnapshot(t.Context(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.planNow = func() time.Time { return time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC) }
	if err := s.saveEndedPlanReports(t.Context(), []planmodel.Plan{p}, a); err != nil {
		t.Fatal(err)
	}
	second, err := s.db.PlanReportSnapshot(t.Context(), scope, run.ID)
	if err != nil || first.BodyHash != second.BodyHash || !first.Report.GeneratedAt.Equal(second.Report.GeneratedAt) || p.State != "enabled" {
		t.Fatal("历史补生成重复改写或停用计划", err)
	}
}
