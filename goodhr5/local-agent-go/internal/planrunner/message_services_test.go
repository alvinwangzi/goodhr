// 本文件验证 HRPlus 活跃服务原来源、最新策略、扫描公平性与最终无工作时结束。
package planrunner

import (
	"encoding/json"
	"goodhr5/local-agent-go/internal/planmodel"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// serviceRunFixture 建立已激活首项和同岗尚未开始的回复项。
func serviceRunFixture(t *testing.T) planmodel.Run {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "planmodel", "testdata", "permit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p planmodel.Permit
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	r := p.Run
	r.Snapshot.Items[1].Actions = []string{"auto_reply"}
	r.Items[1].Snapshot = r.Snapshot.Items[1]
	r.Items[1].Actions = map[string]planmodel.ActionProgress{"auto_reply": {State: "pending"}}
	r.Items[0].State = "running"
	r.Items[0].TaskRunID = "70000000-0000-0000-0000-000000000001"
	for action := range r.Items[0].Actions {
		r.Items[0].Actions[action] = planmodel.ActionProgress{State: "active"}
	}
	return r
}

// servicePlatform 返回已准备项的明确平台，不使用姓名推断。
func servicePlatform(string) (string, error) { return "boss", nil }

// TestMessageServicesLatestPolicyKeepsSource 验证同岗入口去重、未来项不激活以及新策略不改写原工作归属。
func TestMessageServicesLatestPolicyKeepsSource(t *testing.T) {
	r := serviceRunFixture(t)
	s := NewMessageServices("fixture-owner")
	if err := s.Sync(r, servicePlatform); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := s.Next(now, true, true, false)
	if first.ItemRunID != r.Items[0].ID || first.Action != "auto_reply" {
		t.Fatal("未来项提前执行", first)
	}
	if err := s.Observed(first, true, now, false); err != nil {
		t.Fatal(err)
	}
	r.CurrentItem = 1
	r.Sequence++
	r.Items[0].State = "completed"
	r.Items[1].State = "running"
	r.Items[1].TaskRunID = "70000000-0000-0000-0000-000000000002"
	r.Items[1].Actions["auto_reply"] = planmodel.ActionProgress{State: "active"}
	if err := s.Sync(r, servicePlatform); err != nil {
		t.Fatal(err)
	}
	key := MessageServiceKey{"fixture-owner", "boss", r.Items[0].Snapshot.PositionID, "auto_reply"}
	if len(s.services) != 2 || s.services[key].priority {
		t.Fatal("重复入口或未采用最新设置")
	}
	choice := s.Next(now.Add(time.Second), false, true, false)
	if choice.Action == "re_greet" {
		if err := s.Observed(choice, false, now.Add(time.Second), false); err != nil {
			t.Fatal(err)
		}
		choice = s.Next(now.Add(time.Second), false, true, false)
	}
	if choice.ItemRunID != r.Items[0].ID || choice.TaskRunID != r.Items[0].TaskRunID || choice.PolicyItemRunID != r.Items[1].ID {
		t.Fatal("新策略改写原归属", choice)
	}
}

// TestMessageServicesFairScanAndReGreet 验证两批后扫描，到期复打可越过优先回复，复打后仍给回复机会。
func TestMessageServicesFairScanAndReGreet(t *testing.T) {
	r := serviceRunFixture(t)
	s := NewMessageServices("fixture-owner")
	if err := s.Sync(r, servicePlatform); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, action := range []string{"re_greet", "auto_reply"} {
		key := MessageServiceKey{"fixture-owner", "boss", r.Items[0].Snapshot.PositionID, action}
		choice := MessageChoice{Kind: "message", Key: key, Action: action, ItemRunID: r.Items[0].ID, TaskRunID: r.Items[0].TaskRunID, PolicyItemRunID: r.Items[0].ID, ForceCheck: true}
		if err := s.Observed(choice, true, now, false); err != nil {
			t.Fatal(err)
		}
	}
	if s.Next(now, true, true, false).Kind != "scan" {
		t.Fatal("消息阻塞扫描")
	}
	s.Scanned()
	choice := s.Next(now.Add(time.Minute), true, true, false)
	if choice.Action != "re_greet" {
		t.Fatal("到期复打被回复饿死", choice)
	}
	if err := s.Observed(choice, true, now.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if s.Next(now.Add(time.Minute), true, true, false).Action != "auto_reply" {
		t.Fatal("复打后未给回复机会")
	}
}

// TestMessageServicesChecksReGreetDuringReplies 验证持续有回复时仍检查独立复打入口，不等回复全部处理完。
func TestMessageServicesChecksReGreetDuringReplies(t *testing.T) {
	r := serviceRunFixture(t)
	s := NewMessageServices("fixture-owner")
	if err := s.Sync(r, servicePlatform); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	choice := s.Next(now, true, true, false)
	if choice.Action != "auto_reply" {
		t.Fatal("优先回复未先执行", choice)
	}
	if err := s.Observed(choice, true, now, false); err != nil {
		t.Fatal(err)
	}
	if s.Next(now, true, true, false).Action != "re_greet" {
		t.Fatal("复打检查必须等回复完才执行")
	}
}

// TestMessageServicesDefaultAndFinalPass 验证关闭优先回复的周期检查，最终无工作后不等未来周期。
func TestMessageServicesDefaultAndFinalPass(t *testing.T) {
	r := serviceRunFixture(t)
	r.Snapshot.Items[0].PrioritizeReply = false
	r.Items[0].Snapshot = r.Snapshot.Items[0]
	s := NewMessageServices("fixture-owner")
	if err := s.Sync(r, servicePlatform); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 2; i++ {
		choice := s.Next(now, true, false, false)
		if err := s.Observed(choice, false, now, false); err != nil {
			t.Fatal(err)
		}
	}
	s.Scanned()
	if s.Next(now.Add(59*time.Second), true, false, false).Kind != "scan" {
		t.Fatal("关闭优先回复仍反复切页面")
	}
	if s.Next(now.Add(time.Minute), true, false, false).Kind != "message" {
		t.Fatal("消息周期检查未触发")
	}
	for i := 0; i < 2; i++ {
		choice := s.Next(now.Add(time.Minute), false, false, true)
		if !choice.ForceCheck {
			t.Fatal("结束前未检查当前消息")
		}
		if err := s.Observed(choice, false, now.Add(time.Minute), true); err != nil {
			t.Fatal(err)
		}
	}
	if s.Next(now.Add(time.Minute), false, false, true).Kind != "done" {
		t.Fatal("无工作仍等待未来检查")
	}
}

// TestMessageServicesRejectsStaleAndFuture 验证迟到快照、未准备活跃项与平台缺失不能部分覆盖原服务。
func TestMessageServicesRejectsStaleAndFuture(t *testing.T) {
	r := serviceRunFixture(t)
	s := NewMessageServices("fixture-owner")
	lookups := 0
	lookup := func(id string) (string, error) {
		lookups++
		if id != r.Items[0].ID {
			t.Error("读取了未开始项的平台")
		}
		return "boss", nil
	}
	if err := s.Sync(r, lookup); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatal("同一项不同动作重复核对平台", lookups)
	}
	r.Sequence = 2
	if err := s.Sync(r, lookup); err != nil {
		t.Fatal(err)
	}
	r.Sequence = 1
	if err := s.Sync(r, lookup); err == nil {
		t.Fatal("旧序号覆盖新消息编排")
	}
	r.Sequence = 3
	r.Items[1].State = "running"
	r.Items[1].Actions["auto_reply"] = planmodel.ActionProgress{State: "active"}
	if err := s.Sync(r, lookup); err == nil {
		t.Fatal("未来项或无 TaskRun 项被激活")
	}
	if len(s.services) != 2 {
		t.Fatal("错误快照部分覆盖原服务")
	}
}
