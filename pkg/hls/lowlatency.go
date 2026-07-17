package hls

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// part 是一个部分分片(LL-HLS 的 EXT-X-PART),属于当前正在构建的分片(序号 nextSeq)。
type part struct {
	data        []byte
	dur         time.Duration
	independent bool // 是否可独立解码(含关键帧),影响 EXT-X-PART 的 INDEPENDENT=YES
}

// 等待阻塞刷新的最长时间(超时即返回当前播放列表,避免请求悬挂过久)。
const blockingReloadTimeout = 5 * time.Second

// AppendPart 追加一个部分分片到"当前正在构建的分片"(LL-HLS)。independent 表示该 part
// 是否可独立解码(通常本分片首个 part 为 true)。仅在 WithPartTarget 开启时有意义。
func (s *Stream) AppendPart(data []byte, dur time.Duration, independent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.building = append(s.building, part{data: append([]byte(nil), data...), dur: dur, independent: independent})
	s.cond.Broadcast()
}

// CompleteSegment 把当前构建中的各 part 合并成一个完整分片(序号 nextSeq)收官,进入滚动
// 窗口;随后开始累积下一个分片。LL-HLS 专用(与 Append 互斥使用)。
func (s *Stream) CompleteSegment() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || len(s.building) == 0 {
		return nil
	}
	seq := s.nextSeq
	var full []byte
	var dur time.Duration
	for i := range s.building {
		full = append(full, s.building[i].data...)
		dur += s.building[i].dur
	}
	if err := s.cfg.store.Put(seq, full); err != nil {
		return err
	}
	s.nextSeq++
	s.segments = append(s.segments, segMeta{Seq: seq, Duration: dur})
	s.building = s.building[:0]
	for len(s.segments) > s.cfg.window {
		s.cfg.store.Remove(s.segments[0].Seq)
		s.segments = s.segments[1:]
		s.mediaSeq++
	}
	s.cond.Broadcast()
	return nil
}

// llMediaPlaylistLocked 生成 LL-HLS media playlist(调用方已持有读锁)。
func (s *Stream) llMediaPlaylistLocked() []byte {
	partTargetSec := s.llPartTarget.Seconds()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:9\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", s.targetDurationSec())
	// PART-HOLD-BACK 规范建议 ≥ 3×PART-TARGET。
	fmt.Fprintf(&b, "#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.3f\n", 3*partTargetSec)
	fmt.Fprintf(&b, "#EXT-X-PART-INF:PART-TARGET=%.3f\n", partTargetSec)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", s.mediaSeq)
	if s.initSeg != nil {
		b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	}
	// 已完成的分片。
	for i := range s.segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\nseg%d%s\n", s.segments[i].Duration.Seconds(), s.segments[i].Seq, s.cfg.ext)
	}
	// 正在构建分片(序号 nextSeq)的各 part。
	buildSeq := s.nextSeq
	for i := range s.building {
		fmt.Fprintf(&b, "#EXT-X-PART:DURATION=%.3f,URI=\"part%d_%d%s\"", s.building[i].dur.Seconds(), buildSeq, i, s.cfg.ext)
		if s.building[i].independent {
			b.WriteString(",INDEPENDENT=YES")
		}
		b.WriteByte('\n')
	}
	if !s.finished {
		// 预告下一个 part,便于播放端提前发起请求。
		fmt.Fprintf(&b, "#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"part%d_%d%s\"\n", buildSeq, len(s.building), s.cfg.ext)
	} else {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return []byte(b.String())
}

// parseBlockingQuery 解析 LL-HLS 阻塞刷新参数 _HLS_msn(必需)与 _HLS_part(可选)。
func parseBlockingQuery(r *http.Request) (msn uint64, part int, ok bool) {
	q := r.URL.Query()
	msnStr := q.Get("_HLS_msn")
	if msnStr == "" {
		return 0, 0, false
	}
	m, err := strconv.ParseUint(msnStr, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	part = -1
	if p := q.Get("_HLS_part"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			part = v
		}
	}
	return m, part, true
}

// waitForPart 阻塞直到分片序号 msn 的第 part 个 part 就绪(或 msn 已完成/流结束/超时)。
func (s *Stream) waitForPart(msn uint64, part int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	timedOut := false
	timer := time.AfterFunc(blockingReloadTimeout, func() {
		s.mu.Lock()
		timedOut = true
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer timer.Stop()

	for !s.finished && !timedOut && !s.partReadyLocked(msn, part) {
		s.cond.Wait()
	}
}

// partReadyLocked 判断分片 msn 的第 part 个 part 是否已可用(调用方持锁)。
func (s *Stream) partReadyLocked(msn uint64, part int) bool {
	if s.nextSeq > msn {
		return true // 分片 msn 已整段完成
	}
	if s.nextSeq == msn { // msn 正是构建中的分片
		if part < 0 {
			return len(s.building) > 0
		}
		return len(s.building) > part
	}
	return false
}

// partData 按 "part{seq}_{idx}.ext" 取部分分片数据(仅构建中分片的 part 可取)。
func (s *Stream) partData(name string) ([]byte, bool) {
	body := strings.TrimSuffix(strings.TrimPrefix(name, "part"), s.cfg.ext)
	seqStr, idxStr, ok := strings.Cut(body, "_")
	if !ok {
		return nil, false
	}
	seq, err1 := strconv.ParseUint(seqStr, 10, 64)
	idx, err2 := strconv.Atoi(idxStr)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq != s.nextSeq || idx < 0 || idx >= len(s.building) {
		return nil, false
	}
	return s.building[idx].data, true
}
