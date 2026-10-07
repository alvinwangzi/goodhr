// 本文件作用：验证本地"待索要简历名单"的写入去重、状态流转和岗位外键约束。
package localdb

import (
	"strings"
	"testing"
)

// TestCandidateInfoSelectionAndDedup 验证电话、微信、简历意图按稳定 ID 保存，同名不混用，未知结果不重发。
func TestCandidateInfoSelectionAndDedup(t *testing.T) {
	db := openTestDB(t)
	_, err := db.UpsertPositionSnapshot(map[string]any{"id": "p", "name": "岗位", "platform_id": "boss"})
	if err != nil {
		t.Fatal(err)
	}
	one, err := db.EnqueueCandidateInfoRequest("p", "boss", "a", "同名", map[string]bool{"phone": true, "wechat": true})
	if err != nil {
		t.Fatal(err)
	}
	two, err := db.EnqueueCandidateInfoRequest("p", "boss", "b", "同名", map[string]bool{"resume": true})
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID || one.Actions["resume"] || !one.Actions["phone"] {
		t.Fatalf("意图丢失或同名错绑：%+v %+v", one, two)
	}
	if err := db.ClaimCandidateInfoAction(one.ID, "phone"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCandidateInfoResult(one.ID, "phone", "unknown"); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimCandidateInfoAction(one.ID, "phone"); err == nil {
		t.Fatal("未知结果允许再次发送")
	}
	reused, err := db.EnqueueCandidateInfoRequest("p", "boss", "a", "同名", map[string]bool{"phone": true, "wechat": true})
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != one.ID || reused.Results["phone"] != "unknown" {
		t.Fatalf("重入队覆盖了发送状态：%+v", reused)
	}
}

// TestEnqueueResumeRequestDedupesByName 验证同岗位同名候选人只保留一条名单记录。
func TestEnqueueResumeRequestDedupesByName(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "position-1", "name": "Java开发", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	first, err := db.EnqueueResumeRequest("position-1", "boss", "张三")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.EnqueueResumeRequest("position-1", "boss", " 张三 ")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("同岗位同名入队应复用记录，first=%s second=%s", first.ID, second.ID)
	}
	items, err := db.ListResumeRequests("position-1", ResumeRequestStatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v，应只有一条待检查记录", items)
	}
	if items[0].PlatformID != "boss" || items[0].CandidateName != "张三" {
		t.Fatalf("items[0] = %+v", items[0])
	}
}

// TestEnqueueResumeRequestResetsFailed 验证失败记录在候选人再次打招呼时重置为待检查。
func TestEnqueueResumeRequestResetsFailed(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "position-1", "name": "Java开发", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	item, err := db.EnqueueResumeRequest("position-1", "boss", "张三")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MarkResumeFailed(item.ID, "会话列表中未找到该候选人的会话"); err != nil {
		t.Fatal(err)
	}
	reused, err := db.EnqueueResumeRequest("position-1", "boss", "张三")
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != item.ID || reused.Status != ResumeRequestStatusPending || reused.FailReason != "" {
		t.Fatalf("reused = %+v，失败记录应重置为待检查", reused)
	}
}

// TestEnqueueResumeRequestRejectsInvalidInput 验证岗位或姓名为空时入队报错。
func TestEnqueueResumeRequestRejectsInvalidInput(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.EnqueueResumeRequest("", "boss", "张三"); err == nil {
		t.Fatal("岗位 ID 为空时应报错")
	}
	if _, err := db.EnqueueResumeRequest("position-1", "boss", "  "); err == nil {
		t.Fatal("候选人姓名为空时应报错")
	}
}

// TestEnqueueResumeRequestRequiresPosition 验证岗位不存在时受外键约束入队失败。
func TestEnqueueResumeRequestRequiresPosition(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.EnqueueResumeRequest("position-missing", "boss", "张三"); err == nil {
		t.Fatal("岗位不存在时应受外键约束报错")
	} else if !strings.Contains(err.Error(), "写入待索要名单失败") {
		t.Fatalf("err = %v", err)
	}
}

// TestResumeRequestStatusFlow 验证待检查到已完成索要、待检查到失败的状态流转和列表过滤。
func TestResumeRequestStatusFlow(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.UpsertPositionSnapshot(map[string]any{"id": "position-1", "name": "Java开发", "platform_id": "boss"}); err != nil {
		t.Fatal(err)
	}
	okItem, err := db.EnqueueResumeRequest("position-1", "boss", "赵永豪")
	if err != nil {
		t.Fatal(err)
	}
	failItem, err := db.EnqueueResumeRequest("position-1", "boss", "程雨遥")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MarkResumeRequested(okItem.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkResumeFailed(failItem.ID, "回复检查未完成索要"); err != nil {
		t.Fatal(err)
	}
	pending, err := db.ListResumeRequests("position-1", ResumeRequestStatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v，标记后不应还有待检查记录", pending)
	}
	requested, err := db.ListResumeRequests("position-1", ResumeRequestStatusRequested)
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0].CandidateName != "赵永豪" || requested[0].RequestedAt == "" {
		t.Fatalf("requested = %+v", requested)
	}
	failed, err := db.ListResumeRequests("position-1", ResumeRequestStatusFailed)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].CandidateName != "程雨遥" || failed[0].FailReason != "回复检查未完成索要" {
		t.Fatalf("failed = %+v", failed)
	}
	all, err := db.ListResumeRequests("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %+v，不带条件应返回全部记录", all)
	}
}

// TestMarkResumeRequestedRequiresRow 验证标记不存在的记录时报错。
func TestMarkResumeRequestedRequiresRow(t *testing.T) {
	db := openTestDB(t)
	if err := db.MarkResumeRequested("missing-id"); err == nil {
		t.Fatal("标记不存在的记录应报错")
	}
	if err := db.MarkResumeFailed("missing-id", "原因"); err == nil {
		t.Fatal("标记不存在的记录应报错")
	}
}
