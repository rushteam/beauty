package add

import (
	"go/format"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAddConnectPlugin(t *testing.T) {
	in := `version: v2
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go:v1.31.0
    out: gen/go
    opt:
      - paths=source_relative

inputs:
  - directory: api
`
	got, changed := addConnectPlugin(in)
	if !changed {
		t.Fatal("expected changed")
	}
	want := `version: v2
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go:v1.31.0
    out: gen/go
    opt:
      - paths=source_relative
  - remote: ` + connectPluginRemote + `
    out: gen/go
    opt:
      - paths=source_relative

inputs:
  - directory: api
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	if again, changed := addConnectPlugin(got); changed || again != got {
		t.Error("重复追加应为幂等")
	}
}

func TestMiddlewareSourceIsValidGo(t *testing.T) {
	for _, withGrpc := range []bool{false, true} {
		src := middlewareSource("middleware", "Tenant", withGrpc)
		if _, err := format.Source([]byte(src)); err != nil {
			t.Fatalf("grpc=%v: 生成的代码无法解析: %v\n%s", withGrpc, err, src)
		}
		if strings.Contains(src, "UnaryInterceptor") != withGrpc {
			t.Errorf("grpc=%v: 拦截器生成不符合预期", withGrpc)
		}
	}
	if _, err := format.Source([]byte(connectServerSource)); err != nil {
		t.Fatalf("connect 骨架无法解析: %v", err)
	}
}

func TestContribCatalogMatchesRepo(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "contrib")
	var onDisk []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "go.mod" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			onDisk = append(onDisk, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Skipf("读取 contrib 目录失败: %v", err)
	}
	var inCatalog []string
	for _, c := range contribCatalog {
		inCatalog = append(inCatalog, c.Name)
	}
	slices.Sort(onDisk)
	slices.Sort(inCatalog)
	if !slices.Equal(onDisk, inCatalog) {
		t.Errorf("contribCatalog 与仓库 contrib/ 不一致\ncatalog: %v\non disk: %v", inCatalog, onDisk)
	}
}

func TestFindContrib(t *testing.T) {
	if _, ok := findContrib("github.com/rushteam/beauty/contrib/codec/kitex"); !ok {
		t.Error("应支持完整模块路径")
	}
	if _, ok := findContrib("gorn"); ok {
		t.Error("未知模块不应命中")
	}
	if s := suggestContrib("redis"); len(s) != 2 {
		t.Errorf("suggest redis = %v", s)
	}
}
