// 本文件定义 HRPlus 推荐页的标准恢复能力，只传递真实 ID 和瞬时读取提示，不持久化 DOM 或页面位置。
package platformcore

import "context"

// RecommendationCursor 保存切换前的局部事实，StartIndex 只在当前进程中用于读取提示。
type RecommendationCursor struct {
	Valid      bool     `json:"valid"`
	Reason     string   `json:"reason,omitempty"`
	StartIndex int      `json:"start_index"`
	Signature  string   `json:"signature"`
	Anchors    []string `json:"anchors"`
}

// RecommendationResumer 是可选平台能力，平台核对入口和卡片，主流程决定恢复或重扫。
type RecommendationResumer interface {
	CaptureRecommendationCursor(context.Context, Executor, []string) (RecommendationCursor, error)
	ReturnToRecommendation(context.Context, Executor) error
	CheckRecommendationCursor(context.Context, Executor, RecommendationCursor) (bool, string, error)
	RewindRecommendation(context.Context, Executor) error
}
