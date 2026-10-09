// 本文件验证 HRPlus 在已有推荐页只点击沟通菜单，不主动刷新已扫描列表。
package boss

import (
	"context"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/platformcore"
	"testing"
)

// messageNavigationFixture 模拟真实 URL 和唯一菜单，任何页面导航都会被记录。
type messageNavigationFixture struct {
	url           string
	menus         int
	clicks, opens int
	selected      string
	clickErr      error
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
		request := payload.(platformcore.LocatorRequest)
		if len(request.Selector.Selectors) == 1 && request.Selector.Selectors[0] == ".job-select .ui-dropmenu-label" {
			return pageItems(map[string]string{"name": f.selected}), nil
		}
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
		if f.clickErr != nil {
			return nil, f.clickErr
		}
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("不支持的调用 %s", path)
	}
}

// TestJobDropdownClickFailure 验证下拉点击失败立即返回，不再忽略错误后等待并解析旧列表。
func TestJobDropdownClickFailure(t *testing.T) {
	cause := errors.New("下拉不可见")
	f := &messageNavigationFixture{clickErr: cause}
	if _, err := NewRuntime().ResolveReplyTarget(t.Context(), f, "Go"); !errors.Is(err, cause) || f.clicks != 1 {
		t.Fatalf("岗位点击失败被忽略 %v clicks=%d", err, f.clicks)
	}
}

// TestMessageTargetReadOnly 验证当前岗位正确时只读，岗位或页面变化时不给缓存放行。
func TestMessageTargetReadOnly(t *testing.T) {
	f := &messageNavigationFixture{url: "https://www.zhipin.com/web/chat/index", selected: "Go _ 南京 8-12K"}
	target := platformcore.ReplyTarget{PositionID: f.selected, PositionName: "Go", NameUnique: true}
	matched, err := NewRuntime().CheckReplyTarget(t.Context(), f, target)
	if err != nil || !matched || f.clicks != 0 || f.opens != 0 {
		t.Fatalf("只读校验仍进行了页面操作 %v %+v", err, f)
	}
	f.selected = "其他岗位"
	if matched, err = NewRuntime().CheckReplyTarget(t.Context(), f, target); err != nil || matched {
		t.Fatal("错误岗位复用旧目标")
	}
	f.url = "https://www.zhipin.com/web/chat/recommend"
	if matched, err = NewRuntime().CheckReplyTarget(t.Context(), f, target); err != nil || matched {
		t.Fatal("推荐页被当成已准备的沟通页")
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
