// 本文件适配 HRPlus 推荐与沟通切换的局部恢复，所有页面读取和操作均由标准 Worker 能力执行。
package boss

import (
	"context"
	"encoding/json"
	"fmt"
	"goodhr5/local-agent-go/internal/platformcore"
)

// CaptureRecommendationCursor 在离开前核对最近处理者的位置，用完整 ID 保留有序局部事实。
func (r *Runtime) CaptureRecommendationCursor(ctx context.Context, exec platformcore.Executor, anchors []string) (platformcore.RecommendationCursor, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/candidates/capture-anchors", map[string]any{"anchors": anchors, "no_script": true})
	if err != nil {
		return platformcore.RecommendationCursor{}, err
	}
	raw, err := json.Marshal(workerDataMap(result))
	if err != nil {
		return platformcore.RecommendationCursor{}, err
	}
	var cursor platformcore.RecommendationCursor
	err = json.Unmarshal(raw, &cursor)
	return cursor, err
}

// ReturnToRecommendation 点击菜单返回推荐页，避免主动导航导致列表重新加载。
func (r *Runtime) ReturnToRecommendation(ctx context.Context, exec platformcore.Executor) error {
	_, err := exec.Post(ctx, "/api/v1/page/click", platformcore.LocatorRequest{Selector: platformcore.SelectorSpec{Selectors: []string{"dl a"}, Text: "推荐牛人"}})
	if err != nil {
		return err
	}
	return exec.Delay(ctx, "等待推荐菜单切换", 0.5)
}

// CheckRecommendationCursor 只核对原位置对应的最多三个卡片与实际筛选签名。
func (r *Runtime) CheckRecommendationCursor(ctx context.Context, exec platformcore.Executor, cursor platformcore.RecommendationCursor) (bool, string, error) {
	result, err := exec.Post(ctx, "/api/v1/boss/candidates/check-anchors", map[string]any{"cursor": cursor, "no_script": true})
	if err != nil {
		return false, "anchor_read_failed", err
	}
	data := workerDataMap(result)
	return boolFromMap(data, "matched"), stringFromMap(data, "reason"), nil
}

// RewindRecommendation 使用真实滚轮返回当前推荐列表起点，由主流程按已完成 ID 去重。
func (r *Runtime) RewindRecommendation(ctx context.Context, exec platformcore.Executor) error {
	result, err := exec.Post(ctx, "/api/v1/boss/candidates/rewind", map[string]any{"no_script": true})
	if err != nil {
		return err
	}
	if !boolFromMap(workerDataMap(result), "rewound") {
		return fmt.Errorf("推荐列表未确认回到起点")
	}
	return nil
}
