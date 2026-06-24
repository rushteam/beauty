package dev

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Action 开发模式入口：watch=false 时直接运行一次；watch=true 时监听 .go
// 文件变化并自动重启服务。
func Action(ctx context.Context, config string, watch bool) error {
	if !watch {
		return runOnce(ctx, config)
	}
	return runWithWatch(ctx, config)
}

// runOnce 前台运行一次 `go run . -config <config>`。
func runOnce(ctx context.Context, config string) error {
	cmd := exec.CommandContext(ctx, "go", runArgs(config)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runArgs(config string) []string {
	args := []string{"run", "."}
	if config != "" {
		args = append(args, "-config", config)
	}
	return args
}

// runWithWatch 监听工作目录下的 .go 变化，防抖后重启子进程。
func runWithWatch(ctx context.Context, config string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := addDirs(watcher, "."); err != nil {
		return err
	}

	fmt.Println("👀 已进入热重载模式，监听 .go 文件变化 (Ctrl+C 退出)")

	var current *exec.Cmd
	start := func() {
		cmd := exec.Command("go", runArgs(config)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		// 独立进程组，便于连同 `go run` 派生的子进程一起结束
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			fmt.Printf("⚠️  启动失败: %v\n", err)
			return
		}
		current = cmd
	}
	stop := func() {
		if current == nil || current.Process == nil {
			return
		}
		// 结束整个进程组
		_ = syscall.Kill(-current.Process.Pid, syscall.SIGKILL)
		_, _ = current.Process.Wait()
		current = nil
	}

	start()
	defer stop()

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !strings.HasSuffix(event.Name, ".go") {
				continue
			}
			// 新建目录纳入监听
			if event.Op&fsnotify.Create != 0 {
				if fi, statErr := os.Stat(event.Name); statErr == nil && fi.IsDir() {
					_ = addDirs(watcher, event.Name)
				}
			}
			debounce.Reset(300 * time.Millisecond)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Printf("⚠️  监听错误: %v\n", err)
		case <-debounce.C:
			fmt.Println("\n🔄 检测到变更，重启服务...")
			stop()
			start()
		}
	}
}

// addDirs 递归把目录加入监听，跳过隐藏目录与常见无关目录。
func addDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 忽略无法访问的路径
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if path != root && (strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules") {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}
