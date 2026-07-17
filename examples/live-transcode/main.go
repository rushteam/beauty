// live-transcode demo:拓扑 A —— beauty 收 RTMP,把流管道喂给 ffmpeg 转码成多码率
// (ABR)HLS,再用 pkg/hls.Master 组主清单分发。转码交给 ffmpeg(框架不碰编解码),
// beauty 负责采集、鉴权、编排与分发。
//
//	OBS/ffmpeg ──RTMP──▶ pkg/media/rtmp ──重建 FLV──▶ ffmpeg(转码 2 档)──▶ HLS 目录
//	                                                                         │
//	                       pkg/hls.Master(FileServer 变体)◀── 分发 master + 各码率 ──┘
//
// 需要:PATH 里有 ffmpeg。运行:
//
//	go run ./examples/live-transcode
//
// 推流:
//
//	ffmpeg -re -stream_loop -1 -i input.mp4 -c:v libx264 -c:a aac -f flv rtmp://localhost:1935/live/demo
//
// 播放(自适应码率):
//
//	ffplay http://localhost:8090/live/master.m3u8
package main

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/hls"
	"github.com/rushteam/beauty/pkg/media/rtmp"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

const outDir = "./hls-out"

func main() {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("需要 ffmpeg 在 PATH 中")
	}
	for _, v := range []string{"720p", "360p"} {
		if err := os.MkdirAll(filepath.Join(outDir, v), 0o755); err != nil {
			log.Fatal(err)
		}
	}

	// RTMP 采集:每路 publish 起一个 ffmpeg 转码器(这里假设单路,固定输出目录)。
	rtmpSrv := rtmp.NewServer(":1935", func(streamKey string) rtmp.Handler {
		log.Printf("publish: %s → transcoding to %s", streamKey, outDir)
		t, err := newTranscoder(outDir)
		if err != nil {
			log.Printf("start ffmpeg: %v", err)
			return nil // 拒绝
		}
		return t
	}, rtmp.WithServiceName("rtmp-ingest"))

	// ABR 主清单:两档变体各由一个(设置好 content-type 的)FileServer 分发。
	master := hls.NewMaster(
		hls.Variant{Name: "720p", Bandwidth: 2500000, Resolution: "1280x720", Handler: mediaDir(filepath.Join(outDir, "720p"))},
		hls.Variant{Name: "360p", Bandwidth: 800000, Resolution: "640x360", Handler: mediaDir(filepath.Join(outDir, "360p"))},
	)
	mux := http.NewServeMux()
	mux.Handle("/live/", http.StripPrefix("/live", master))

	app := beauty.New(
		beauty.WithService(rtmpSrv),
		beauty.WithWebServer(":8090", mux, webserver.WithServiceName("hls-origin")),
	)
	log.Println("RTMP :1935 | ABR play http://localhost:8090/live/master.m3u8")
	if err := app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// transcoder 实现 rtmp.Handler:把 RTMP tag 重建成 FLV 流写进 ffmpeg stdin。
type transcoder struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	flv   *flvWriter
}

func newTranscoder(dir string) (*transcoder, error) {
	// 一个 ffmpeg 进程,两路输出(720p / 360p)HLS,各自 delete_segments 滚动窗口。
	args := []string{"-hide_banner", "-loglevel", "warning", "-f", "flv", "-i", "pipe:0"}
	for _, v := range []struct {
		name, br, size string
	}{
		{"720p", "2500k", "1280x720"},
		{"360p", "800k", "640x360"},
	} {
		args = append(args,
			"-map", "0:v", "-map", "0:a",
			"-c:v", "libx264", "-preset", "veryfast", "-b:v", v.br, "-s", v.size,
			"-c:a", "aac", "-b:a", "128k",
			"-f", "hls", "-hls_time", "2", "-hls_list_size", "6", "-hls_flags", "delete_segments",
			filepath.Join(dir, v.name, "index.m3u8"),
		)
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &transcoder{cmd: cmd, stdin: stdin, flv: &flvWriter{w: stdin}}
	if err := t.flv.header(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *transcoder) OnMetaData(p []byte)               { _ = t.flv.tag(18, 0, p) }
func (t *transcoder) OnAudio(ts uint32, d []byte) error { return t.flv.tag(8, ts, d) }
func (t *transcoder) OnVideo(ts uint32, d []byte) error { return t.flv.tag(9, ts, d) }
func (t *transcoder) OnClose() {
	_ = t.stdin.Close() // 关 stdin → ffmpeg 收到 EOF 退出
	_ = t.cmd.Wait()
}

// flvWriter 把 RTMP 收到的 tag body 重新拼成合法 FLV 流(FLV header + tag)。
type flvWriter struct{ w io.Writer }

func (f *flvWriter) header() error {
	// "FLV" v1, flags=0x05(音频+视频), header size 9, PreviousTagSize0=0
	_, err := f.w.Write([]byte{'F', 'L', 'V', 1, 0x05, 0, 0, 0, 9, 0, 0, 0, 0})
	return err
}

func (f *flvWriter) tag(tagType byte, ts uint32, body []byte) error {
	n := len(body)
	hdr := []byte{
		tagType,
		byte(n >> 16), byte(n >> 8), byte(n), // DataSize u24
		byte(ts >> 16), byte(ts >> 8), byte(ts), byte(ts >> 24), // Timestamp u24 + Extended
		0, 0, 0, // StreamID
	}
	if _, err := f.w.Write(hdr); err != nil {
		return err
	}
	if _, err := f.w.Write(body); err != nil {
		return err
	}
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(11+n))
	_, err := f.w.Write(prev[:])
	return err
}

// mediaDir 是设置了 HLS content-type 的静态文件分发(FileServer 不认 .m3u8/.ts)。
func mediaDir(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasSuffix(r.URL.Path, ".ts"):
			w.Header().Set("Content-Type", "video/mp2t")
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fs.ServeHTTP(w, r)
	})
}
