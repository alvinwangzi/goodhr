// 本文件验证 HRPlus 在已有推荐页只点击沟通菜单，不主动刷新已扫描列表。
package boss

import (
	"context"
	"fmt"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// messageNavigationFixture 模拟真实 URL 和唯一菜单，任何页面导航都会被记录。
type messageNavigationFixture struct {
	url           string
	menus         int
	clicks, opens int
}

// Post 返回页面事实并记录标准菜单操作，不能使用脚本注入或不明确的定位。
func (f *messageNavigationFixture) Post(_ context.Context, path string, payload any) (map[string]any, error) {
	switch path {
	case "/api/v1/page/list":
		return map[string]any{"pages": []any{map[string]any{"url": f.url, "is_default": true}}}, nil
	case "/api/v1/page/open":
		f.opens++
		return map[string]any{}, nil
	case "/api/v1/page/find-elements":
		items := []map[string]string{}
		for i := 0; i < f.menus; i++ {
			items = append(items, map[string]string{})
		}
		return pageItems(items...), nil
	case "/api/v1/page/click":
		if _, ok := payload.(platformcore.LocatorRequest); !ok {
			return nil, fmt.Errorf("菜单必须使用标准强类型定位")
		}
		f.clicks++
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("不支持的调用 %s", path)
	}
}

// Log 测试中不输出候选人内容。
func (*messageNavigationFixture) Log(string, string) {}

// Delay 不引入实际等待，只遵循上下文取消。
func (*messageNavigationFixture) Delay(ctx context.Context, _ string, _ float64) error {
	return ctx.Err()
}

// TestMessageNavigationPreservesRecommendation 验证已有推荐页通过菜单切换，沟通页重复准备不导航。
func TestMessageNavigationPreservesRecommendation(t *testing.T) {
	runtime := NewRuntime()
	f := &messageNavigationFixture{url: "https://www.zhipin.com/web/chat/recommend", menus: 1}
	if err := runtime.PrepareReplyPage(t.Context(), f); err != nil || f.clicks != 1 || f.opens != 0 {
		t.Fatalf("不应刷新推荐进度 %+v %v", f, err)
	}
	f.url = "https://www.zhipin.com/web/chat/index"
	if err := runtime.PrepareReplyPage(t.Context(), f); err != nil || f.clicks != 1 || f.opens != 0 {
		t.Fatalf("消息页重复准备不应导航 %+v %v", f, err)
	}
}

// TestAmbiguousMessageMenuDoesNotReload 验证菜单缺失或重复不能偷偷用页面导航覆盖原进度。
func TestAmbiguousMessageMenuDoesNotReload(t *testing.T) {
	for _, count := range []int{0, 2} {
		f := &messageNavigationFixture{url: "https://www.zhipin.com/web/chat/recommend", menus: count}
		if err := NewRuntime().PrepareReplyPage(t.Context(), f); err == nil || f.clicks != 0 || f.opens != 0 {
			t.Fatalf("异常菜单仍操作 %+v %v", f, err)
		}
	}
}
