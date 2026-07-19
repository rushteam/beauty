// Package buildinfo 在运行时暴露构建信息(版本 / commit / 构建时间 / Go 版本 / 模块 / 是否
// dirty),用于 /version 端点、启动日志、诊断。零依赖(仅标准库)。
//
// 两个来源自动合并:
//   - ldflags 注入的包级变量(优先):
//     go build -ldflags "-X github.com/rushteam/beauty/pkg/buildinfo.version=1.2.3 \
//     -X github.com/rushteam/beauty/pkg/buildinfo.commit=$(git rev-parse HEAD) \
//     -X github.com/rushteam/beauty/pkg/buildinfo.buildTime=$(date -u +%FT%TZ)"
//   - runtime/debug.ReadBuildInfo(回退):从 VCS 元数据取 revision/time/modified,
//     以及主模块路径与版本(go build 在 VCS 检出中会自动嵌入 vcs.*)。
//
// 未注入 ldflags 且非 VCS 构建时,尽力给出可用信息,Version 兜底 "unknown"。
package buildinfo

import (
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
)

// 以下包级变量供 ldflags -X 注入(必须是 var、非 const)。
var (
	version   string
	commit    string
	buildTime string
)

// Info 是聚合后的构建信息。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Module    string `json:"module"`
	Dirty     bool   `json:"dirty"` // 构建时工作区有未提交改动(vcs.modified)
}

// Get 读取并合并构建信息(ldflags 优先,缺失项用 runtime/debug 回退)。
func Get() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.Module = bi.Main.Path
		if info.Version == "" && bi.Main.Version != "" {
			info.Version = bi.Main.Version // 如 v1.2.3 或 (devel)
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.BuildTime == "" {
					info.BuildTime = s.Value
				}
			case "vcs.modified":
				info.Dirty = s.Value == "true"
			}
		}
	}
	if info.Version == "" {
		info.Version = "unknown"
	}
	return info
}

// Short 返回短 commit(前 12 位),便于日志展示。
func (i Info) Short() string {
	if len(i.Commit) > 12 {
		return i.Commit[:12]
	}
	return i.Commit
}

// String 返回单行摘要,适合启动日志。
func (i Info) String() string {
	var b strings.Builder
	b.WriteString("version=")
	b.WriteString(i.Version)
	if c := i.Short(); c != "" {
		b.WriteString(" commit=")
		b.WriteString(c)
		if i.Dirty {
			b.WriteString("-dirty")
		}
	}
	if i.BuildTime != "" {
		b.WriteString(" built=")
		b.WriteString(i.BuildTime)
	}
	b.WriteString(" go=")
	b.WriteString(i.GoVersion)
	return b.String()
}

// Handler 返回一个把构建信息以 JSON 输出的 http.Handler,可挂到 /version。
func Handler() http.HandlerFunc {
	info := Get()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(info)
	}
}
