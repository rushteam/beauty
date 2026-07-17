package hls_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hls"
)

func TestLLHLS_PlaylistDirectives(t *testing.T) {
	s := hls.NewStream(hls.WithPartTarget(300*time.Millisecond), hls.WithTargetDuration(2*time.Second))

	s.AppendPart([]byte("p0"), 300*time.Millisecond, true)
	s.AppendPart([]byte("p1"), 300*time.Millisecond, false)

	pl := string(s.MediaPlaylist())
	for _, want := range []string{
		"#EXT-X-VERSION:9",
		"#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=0.900",
		"#EXT-X-PART-INF:PART-TARGET=0.300",
		`#EXT-X-PART:DURATION=0.300,URI="part0_0.ts",INDEPENDENT=YES`,
		`#EXT-X-PART:DURATION=0.300,URI="part0_1.ts"`,
		`#EXT-X-PRELOAD-HINT:TYPE=PART,URI="part0_2.ts"`,
	} {
		if !strings.Contains(pl, want) {
			t.Fatalf("LL playlist 缺 %q\n%s", want, pl)
		}
	}

	// 收官后:出现完整分片 seg0,building 清空,下一分片(seq1)预告。
	if err := s.CompleteSegment(); err != nil {
		t.Fatalf("complete: %v", err)
	}
	pl = string(s.MediaPlaylist())
	if !strings.Contains(pl, "#EXTINF:0.600,\nseg0.ts") {
		t.Fatalf("收官后应有 seg0(0.6s)\n%s", pl)
	}
	if strings.Contains(pl, "part0_") {
		t.Fatalf("收官后不应再列 seg0 的 part\n%s", pl)
	}
	if !strings.Contains(pl, `#EXT-X-PRELOAD-HINT:TYPE=PART,URI="part1_0.ts"`) {
		t.Fatalf("应预告下一分片的 part\n%s", pl)
	}
}

func TestLLHLS_ServePart(t *testing.T) {
	s := hls.NewStream(hls.WithPartTarget(300 * time.Millisecond))
	s.AppendPart([]byte("PART-BYTES"), 300*time.Millisecond, true)

	srv := httptest.NewServer(http.StripPrefix("/live", s))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/live/part0_0.ts")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	if string(buf[:n]) != "PART-BYTES" {
		t.Fatalf("part body = %q", buf[:n])
	}
}

func TestLLHLS_BlockingReload(t *testing.T) {
	s := hls.NewStream(hls.WithPartTarget(300 * time.Millisecond))

	// 请求"分片0的part0",此时还没有任何 part,应阻塞。
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/index.m3u8?_HLS_msn=0&_HLS_part=0", nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("part 未就绪时应阻塞")
	case <-time.After(150 * time.Millisecond):
	}

	// 追加 part0 → 阻塞请求应返回。
	s.AppendPart([]byte("p0"), 300*time.Millisecond, true)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("part 就绪后阻塞请求应返回")
	}
}
