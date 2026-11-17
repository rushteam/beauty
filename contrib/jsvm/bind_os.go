package jsvm

import (
	"bytes"
	"errors"
	"os"
	"os/exec"

	"github.com/dop251/goja"
)

// bindOS 绑定 $os(需 WithOS(true))。路径不做任何限制,等同进程权限。
// exec(name, ...args) 不经过 shell,返回 {stdout, stderr, exitCode};命令随当前调用的 ctx 一起被取消。
func (inst *instance) bindOS() error {
	rt := inst.rt
	obj := rt.NewObject()
	if err := setAll(obj, map[string]any{
		"getenv":   os.Getenv,
		"readFile": func(name string) (string, error) { b, err := os.ReadFile(name); return string(b), err },
		"writeFile": func(name string, data goja.Value) error {
			b, ok := inst.toBytes(data)
			if !ok {
				panic(rt.NewTypeError("$os.writeFile: data must be a string or binary"))
			}
			return os.WriteFile(name, b, 0o644)
		},
		"readDir": func(name string) ([]any, error) {
			entries, err := os.ReadDir(name)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(entries))
			for i, e := range entries {
				out[i] = map[string]any{"name": e.Name(), "isDir": e.IsDir()}
			}
			return out, nil
		},
		"mkdirAll":  func(name string) error { return os.MkdirAll(name, 0o755) },
		"remove":    os.Remove,
		"removeAll": os.RemoveAll,
		"exec": func(name string, args ...string) map[string]any {
			cmd := exec.CommandContext(inst.ctx, name, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					inst.throw(err)
				}
				code = ee.ExitCode()
			}
			return map[string]any{"stdout": stdout.String(), "stderr": stderr.String(), "exitCode": code}
		},
	}); err != nil {
		return err
	}
	return rt.Set("$os", obj)
}
