package hlsmux

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/media"
)

// TestBridge_WithHub:Bridge 接入 pkg/media.Hub 做多路管理——Acquire/按 key 路由播放/Release。
func TestBridge_WithHub(t *testing.T) {
	hub := media.NewHub(func(string) *Bridge {
		return NewBridge(WithVariant(VariantMPEGTS), WithSegmentMinDuration(100*time.Millisecond))
	})

	sess, ok := hub.Acquire("roomA")
	if !ok {
		t.Fatal("Acquire 应成功")
	}
	feed(t, sess.Stream, 16) // 经该路 Session 的 Bridge 喂帧

	// 经 Hub 按 key 路由播放。
	if rec := serve(t, hub, "/roomA/index.m3u8"); rec.Code != http.StatusOK {
		t.Fatalf("路由播放 status = %d, want 200", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("播放列表缺少 #EXTM3U:%q", rec.Body.String())
	}

	// 未知 key → 404。
	if rec := serve(t, hub, "/ghost/index.m3u8"); rec.Code != http.StatusNotFound {
		t.Fatalf("未知流 status = %d, want 404", rec.Code)
	}

	// Release 收尾并回收(Bridge.Finish 幂等,不 panic)。
	hub.Release("roomA")
	if n := hub.Count(); n != 0 {
		t.Fatalf("Release 后 count = %d, want 0", n)
	}
	hub.Release("roomA") // 幂等
}

// TestBridge_AsABRVariant:Bridge 作为 hls.Master 的一路码率变体(ABR)——Master 生成主清单
// 并按变体名路由到 Bridge。
func TestBridge_AsABRVariant(t *testing.T) {
	b := NewBridge(WithVariant(VariantMPEGTS), WithSegmentMinDuration(100*time.Millisecond))
	feed(t, b, 16)

	master := hls.NewMaster(hls.Variant{
		Name:       "720p",
		Bandwidth:  2_500_000,
		Resolution: "1280x720",
		Handler:    b, // Bridge 是 http.Handler,直接当变体
	})

	// 主清单列出该变体。
	rec := serve(t, master, "/master.m3u8")
	if rec.Code != http.StatusOK {
		t.Fatalf("master status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "#EXT-X-STREAM-INF") || !strings.Contains(body, "720p/index.m3u8") {
		t.Fatalf("master playlist 未含变体:%q", body)
	}

	// 变体路由到 Bridge。
	if rec := serve(t, master, "/720p/index.m3u8"); rec.Code != http.StatusOK {
		t.Fatalf("变体路由 status = %d, want 200", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("变体播放列表缺少 #EXTM3U:%q", rec.Body.String())
	}
	b.OnClose()
}
