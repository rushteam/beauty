package hls_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hls"
)

func TestMaster_PlaylistAndRouting(t *testing.T) {
	hi := hls.NewStream()
	lo := hls.NewStream()
	hi.Append([]byte("HI-SEG"), time.Second)
	lo.Append([]byte("LO-SEG"), time.Second)

	master := hls.NewMaster(
		hls.Variant{Name: "720p", Bandwidth: 2500000, Resolution: "1280x720", Codecs: "avc1.64001f,mp4a.40.2", Handler: hi},
		hls.Variant{Name: "360p", Bandwidth: 800000, Resolution: "640x360", Handler: lo},
	)

	// master playlist
	pl := string(master.Playlist())
	for _, want := range []string{
		"#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720,CODECS=\"avc1.64001f,mp4a.40.2\"",
		"720p/index.m3u8",
		"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360",
		"360p/index.m3u8",
	} {
		if !strings.Contains(pl, want) {
			t.Fatalf("master 缺 %q\n%s", want, pl)
		}
	}

	srv := httptest.NewServer(http.StripPrefix("/live", master))
	defer srv.Close()

	// 根 master
	resp, _ := http.Get(srv.URL + "/live/master.m3u8")
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("master content-type = %q", ct)
	}
	resp.Body.Close()

	// 路由到变体的分片
	resp, err := http.Get(srv.URL + "/live/720p/seg0.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	if string(buf[:n]) != "HI-SEG" {
		t.Fatalf("720p seg0 = %q, want HI-SEG", buf[:n])
	}

	// 路由到另一变体的 media playlist
	resp, _ = http.Get(srv.URL + "/live/360p/index.m3u8")
	body := make([]byte, 256)
	n, _ = resp.Body.Read(body)
	resp.Body.Close()
	if !strings.Contains(string(body[:n]), "seg0.ts") {
		t.Fatalf("360p media playlist 异常: %q", body[:n])
	}

	// 未知变体 → 404
	resp, _ = http.Get(srv.URL + "/live/1080p/index.m3u8")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知变体应 404, got %d", resp.StatusCode)
	}
}
