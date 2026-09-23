// 本文件验证候选人消息身份、岗位归属及防重复指纹，拒绝不明确的页面事实。
package platformcore

import "testing"

// validReplyContext 返回具有稳定标识的文本会话。
func validReplyContext() ReplyContext {
	return ReplyContext{Conversation: ReplyConversation{ID: "c1", PositionID: "job1", PositionName: "Go", Name: "同名"}, Messages: []ReplyMessage{{ID: "m1", Direction: "inbound", Kind: "text", Text: "你好"}}}
}

// TestReplyContextRejectsUnsafeMessages 验证方向、类型、岗位和稳定身份缺失时不生成回复。
func TestReplyContextRejectsUnsafeMessages(t *testing.T) {
	target := ReplyTarget{PositionID: "job1", PositionName: "Go"}
	for _, tc := range []struct {
		name   string
		change func(*ReplyContext)
	}{
		{"没有稳定会话", func(c *ReplyContext) { c.Conversation.ID = "" }},
		{"岗位不同", func(c *ReplyContext) { c.Conversation.PositionID = "job2" }},
		{"仅名称但不唯一", func(c *ReplyContext) { c.Conversation.PositionID = "" }},
		{"我方消息", func(c *ReplyContext) { c.Messages[0].Direction = "outbound" }},
		{"未知方向", func(c *ReplyContext) { c.Messages[0].Direction = "" }},
		{"附件", func(c *ReplyContext) { c.Messages[0].Kind = "attachment" }},
		{"系统通知", func(c *ReplyContext) { c.Messages[0].Kind = "system" }},
		{"空消息", func(c *ReplyContext) { c.Messages[0].Text = " " }},
		// 注意：ID 为空不再拒绝——自适应指纹会退化为正文哈希（Boss 等无稳定 ID 平台需要此回退）
		// 只有会话 ID 为空（没有稳定会话）才拒绝
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := validReplyContext()
			tc.change(&value)
			if _, err := ValidateReplyContext(value, target); err == nil {
				t.Fatal("不安全会话未拦截")
			}
		})
	}
}

// TestReplyFingerprints 验证防重身份跨上下文稳定，但新增消息和不同会话不会混淆。
func TestReplyFingerprints(t *testing.T) {
	target := ReplyTarget{PositionID: "job1", PositionName: "Go"}
	original, err := ValidateReplyContext(validReplyContext(), target)
	if err != nil {
		t.Fatal(err)
	}
	changed := validReplyContext()
	changed.Messages[0].Text = "更新消息"
	next, err := ValidateReplyContext(changed, target)
	if err != nil {
		t.Fatal(err)
	}
	if next.InboundFingerprint != original.InboundFingerprint {
		t.Fatal("有消息 ID 时不应随正文更换入站身份")
	}
	if next.Fingerprint == original.Fingerprint {
		t.Fatal("上下文变更未被检测")
	}
	changed.Conversation.ID = "c2"
	other, _ := ValidateReplyContext(changed, target)
	if other.InboundFingerprint == original.InboundFingerprint {
		t.Fatal("不同会话指纹相同")
	}
	changed = validReplyContext()
	changed.Messages[0].ID = ""
	changed.Messages[0].Timestamp = "2026-09-23T12:00:00Z"
	if _, err := ValidateReplyContext(changed, target); err != nil {
		t.Fatal(err)
	}
	changed.Messages[0].Timestamp = "刚刚"
	// 不稳定时间 + 空 ID：自适应指纹退化为正文哈希，仍然有效（Boss 场景）
	if _, err := ValidateReplyContext(changed, target); err != nil {
		t.Fatalf("正文回退指纹应有效: %v", err)
	}
	// 正文为空时才真正无法生成指纹
	changed.Messages[0].Text = ""
	if _, err := ValidateReplyContext(changed, target); err == nil {
		t.Fatal("正文为空时不应生成指纹")
	}
}

// TestReplyUniquePositionName 验证只有明确唯一且完全一致的名称才能替代岗位标识。
func TestReplyUniquePositionName(t *testing.T) {
	value := validReplyContext()
	value.Conversation.PositionID = ""
	if _, err := ValidateReplyContext(value, ReplyTarget{PositionName: "Go", NameUnique: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateReplyContext(value, ReplyTarget{PositionName: "Go 工程师", NameUnique: true}); err == nil {
		t.Fatal("不能模糊匹配岗位")
	}
}
