package mdrt

import (
	"io"
	"io/fs"
	"path"
	"sort"
	"sync/atomic"
	"time"
)

// snapshot 是一代不可变的生成脚本。
type snapshot struct {
	files map[string]string
}

// genFS 把当前快照暴露成 fs.FS。只有提交协程会替换指针。
type genFS struct {
	cur atomic.Pointer[snapshot]
}

func newGenFS() *genFS {
	g := &genFS{}
	g.cur.Store(&snapshot{files: map[string]string{}})
	return g
}

func (g *genFS) Store(s *snapshot) { g.cur.Store(s) }

func (g *genFS) Open(name string) (fs.File, error) {
	name = path.Clean(name)
	s := g.cur.Load()
	if name == "." {
		return &snapDir{entries: s.entries()}, nil
	}
	body, ok := s.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &snapFile{name: name, data: []byte(body)}, nil
}

func (g *genFS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = path.Clean(name)
	if name != "." {
		return nil, fs.ErrNotExist
	}
	return g.cur.Load().entries(), nil
}

func (s *snapshot) entries() []fs.DirEntry {
	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, dirEntry{name: name, size: int64(len(s.files[name]))})
	}
	return out
}

type dirEntry struct {
	name string
	size int64
}

func (d dirEntry) Name() string               { return d.name }
func (d dirEntry) IsDir() bool                { return false }
func (d dirEntry) Type() fs.FileMode          { return 0 }
func (d dirEntry) Info() (fs.FileInfo, error) { return fileInfo{name: d.name, size: d.size}, nil }

type fileInfo struct {
	name string
	size int64
}

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) Size() int64        { return f.size }
func (f fileInfo) Mode() fs.FileMode  { return 0o644 }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return false }
func (f fileInfo) Sys() any           { return nil }

type snapFile struct {
	name string
	data []byte
	off  int
}

func (f *snapFile) Stat() (fs.FileInfo, error) {
	return fileInfo{name: f.name, size: int64(len(f.data))}, nil
}
func (f *snapFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}
func (f *snapFile) Close() error { return nil }

type snapDir struct {
	entries []fs.DirEntry
	off     int
}

func (d *snapDir) Stat() (fs.FileInfo, error) {
	return fileInfo{name: ".", size: 0}, nil
}
func (d *snapDir) Read([]byte) (int, error) { return 0, io.EOF }
func (d *snapDir) Close() error             { return nil }
func (d *snapDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.off >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 {
		out := d.entries[d.off:]
		d.off = len(d.entries)
		return out, nil
	}
	end := d.off + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	out := d.entries[d.off:end]
	d.off = end
	return out, nil
}
