package hls_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/hls"
)

func TestStream_PlaylistAndWindow(t *testing.T) {
	s := hls.NewStream(hls.WithWindow(3), hls.WithTargetDuration(2*time.Second))

	for i := range 5 {
		s.Append([]byte{byte(i)}, 2*time.Second)
	}

	pl := string(s.MediaPlaylist())
	// 窗口=3:应只保留 seg2/3/4,media-sequence=2。
	if !strings.Contains(pl, "#EXT-X-MEDIA-SEQUENCE:2") {
		t.Fatalf("media-sequence 应为 2\n%s", pl)
	}
	for _, want := range []string{"seg2.ts", "seg3.ts", "seg4.ts"} {
		if !strings.Contains(pl, want) {
			t.Fatalf("playlist 缺 %s\n%s", want, pl)
		}
	}
	for _, gone := range []string{"seg0.ts", "seg1.ts"} {
		if strings.Contains(pl, gone) {
			t.Fatalf("%s 应已被淘汰\n%s", gone, pl)
		}
	}
	if !strings.Contains(pl, "#EXT-X-TARGETDURATION:2") {
		t.Fatalf("target duration 应为 2\n%s", pl)
	}
	if strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Fatal("直播中不应有 ENDLIST")
	}

	// 被淘汰的分片取不到,窗口内的取得到。
	if _, ok := s.SegmentData(0); ok {
		t.Fatal("seg0 应已淘汰")
	}
	if b, ok := s.SegmentData(4); !ok || len(b) != 1 || b[0] != 4 {
		t.Fatalf("seg4 数据错: %v ok=%v", b, ok)
	}
}

func TestStream_FinishBecomesVOD(t *testing.T) {
	s := hls.NewStream(hls.WithWindow(10))
	s.Append([]byte("a"), time.Second)
	s.Append([]byte("b"), time.Second)
	s.Finish()

	pl := string(s.MediaPlaylist())
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Fatalf("Finish 后应有 ENDLIST\n%s", pl)
	}
	if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Fatalf("Finish 后应为 VOD\n%s", pl)
	}
	// Finish 后 Append 无效。
	if seq, _ := s.Append([]byte("c"), time.Second); seq != 0 {
		t.Fatalf("Finish 后 Append 应无效, got seq=%d", seq)
	}
}

func TestStream_ServeHTTP(t *testing.T) {
	s := hls.NewStream()
	s.Append([]byte("SEGMENT-DATA"), 1500*time.Millisecond)

	srv := httptest.NewServer(http.StripPrefix("/live", s))
	defer srv.Close()

	// 播放列表
	resp, err := http.Get(srv.URL + "/live/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("m3u8 content-type = %q", ct)
	}
	resp.Body.Close()

	// 分片
	resp, err = http.Get(srv.URL + "/live/seg0.ts")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("segment content-type = %q", ct)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	if string(buf[:n]) != "SEGMENT-DATA" {
		t.Fatalf("segment body = %q", buf[:n])
	}

	// 不存在的分片 → 404
	resp, err = http.Get(srv.URL + "/live/seg999.ts")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("缺失分片应 404, got %d", resp.StatusCode)
	}
}

func TestStream_DiskStore(t *testing.T) {
	dir := t.TempDir()
	store, err := hls.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("disk store: %v", err)
	}
	s := hls.NewStream(hls.WithWindow(2), hls.WithStore(store))

	for i := range 4 {
		if _, err := s.Append([]byte{byte('A' + i)}, time.Second); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// 窗口=2:seg2/3 在,seg0/1 已从磁盘淘汰。
	if b, ok := s.SegmentData(3); !ok || b[0] != 'D' {
		t.Fatalf("seg3 = %v ok=%v", b, ok)
	}
	if _, ok := s.SegmentData(0); ok {
		t.Fatal("seg0 应已从磁盘淘汰")
	}
	if _, err := os.Stat(filepath.Join(dir, "seg0.dat")); !os.IsNotExist(err) {
		t.Fatal("seg0.dat 文件应已删除")
	}
	if _, err := os.Stat(filepath.Join(dir, "seg3.dat")); err != nil {
		t.Fatalf("seg3.dat 应在磁盘: %v", err)
	}
}

func TestStream_FMP4InitSegment(t *testing.T) {
	s := hls.NewStream(hls.WithSegmentExt(".m4s"))
	s.SetInitSegment([]byte("INIT"))
	s.Append([]byte("x"), time.Second)

	pl := string(s.MediaPlaylist())
	if !strings.Contains(pl, "#EXT-X-VERSION:7") {
		t.Fatalf("fMP4 应为 version 7\n%s", pl)
	}
	if !strings.Contains(pl, `#EXT-X-MAP:URI="init.mp4"`) {
		t.Fatalf("应含 EXT-X-MAP\n%s", pl)
	}
	if !strings.Contains(pl, "seg0.m4s") {
		t.Fatalf("分片应为 .m4s\n%s", pl)
	}

	srv := httptest.NewServer(s)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/init.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("init content-type = %q", ct)
	}
}
