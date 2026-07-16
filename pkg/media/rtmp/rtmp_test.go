package rtmp_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	gortmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/rushteam/beauty/pkg/media/rtmp"
)

type recHandler struct {
	audio, video chan []byte
	closed       chan struct{}
}

func (h *recHandler) OnMetaData([]byte) {}
func (h *recHandler) OnAudio(_ uint32, d []byte) error {
	h.audio <- append([]byte(nil), d...)
	return nil
}
func (h *recHandler) OnVideo(_ uint32, d []byte) error {
	h.video <- append([]byte(nil), d...)
	return nil
}
func (h *recHandler) OnClose() { close(h.closed) }

func TestServer_IngestPublish(t *testing.T) {
	rec := &recHandler{
		audio:  make(chan []byte, 4),
		video:  make(chan []byte, 4),
		closed: make(chan struct{}),
	}
	var (
		mu     sync.Mutex
		gotKey string
	)
	srv := rtmp.NewServer("127.0.0.1:0", func(key string) rtmp.Handler {
		mu.Lock()
		gotKey = key
		mu.Unlock()
		return rec
	}, rtmp.WithServiceName("test-ingest"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	select {
	case <-srv.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("server 未就绪")
	}
	defer func() { cancel(); <-done }()

	// ---- 推流客户端(go-rtmp client)----
	silent := logrus.New()
	silent.SetOutput(io.Discard)
	cc, err := gortmp.Dial("rtmp", srv.Addr().String(), &gortmp.ConnConfig{Logger: silent})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cc.Close()

	if err := cc.Connect(nil); err != nil {
		t.Fatalf("connect: %v", err)
	}
	stream, err := cc.CreateStream(nil, 128)
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if err := stream.Publish(&rtmpmsg.NetStreamPublish{PublishingName: "testkey", PublishingType: "live"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// 发一帧视频、一帧音频(csid: 视频 6 / 音频 4 是惯例)。
	if err := stream.Write(6, 0, &rtmpmsg.VideoMessage{Payload: bytes.NewReader([]byte("VIDEOFRAME"))}); err != nil {
		t.Fatalf("write video: %v", err)
	}
	if err := stream.Write(4, 0, &rtmpmsg.AudioMessage{Payload: bytes.NewReader([]byte("AUDIOFRAME"))}); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	select {
	case v := <-rec.video:
		if string(v) != "VIDEOFRAME" {
			t.Fatalf("video = %q, want VIDEOFRAME", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到视频帧")
	}
	select {
	case a := <-rec.audio:
		if string(a) != "AUDIOFRAME" {
			t.Fatalf("audio = %q, want AUDIOFRAME", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到音频帧")
	}

	mu.Lock()
	key := gotKey
	mu.Unlock()
	if key != "testkey" {
		t.Fatalf("publish key = %q, want testkey", key)
	}
}
