package jsvm

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/dop251/goja"
)

// moduleRoot 是 require 的一个查找根。id 在 VM 生命周期内稳定。
type moduleRoot struct {
	id   int
	fsys fs.FS
}

// fileOrigin 描述一个脚本文件属于哪个模块根。root 为 nil 表示内联脚本。
type fileOrigin struct {
	root *moduleRoot
	file string
}

// loadedModule 是一个实例内 require 的缓存项。循环依赖时返回当时的 exports。
type loadedModule struct {
	exports goja.Value
}

// indexRoots 给每个带文件系统的入口和一个 WithModuleFS 分配模块根。
// 查找顺序即注册顺序:先 WithDir/WithFS,再 WithModuleFS。
func (vm *VM) indexRoots() {
	next := 1
	for i := range vm.cfg.sources {
		s := &vm.cfg.sources[i]
		if s.fsys == nil {
			continue
		}
		vm.roots = append(vm.roots, &moduleRoot{id: next, fsys: s.fsys})
		s.rootID = next
		next++
	}
	for _, fsys := range vm.cfg.moduleFS {
		vm.roots = append(vm.roots, &moduleRoot{id: next, fsys: fsys})
		next++
	}
}

func (vm *VM) rootByID(id int) *moduleRoot {
	for _, r := range vm.roots {
		if r.id == id {
			return r
		}
	}
	return nil
}

// scriptName 是 goja 程序名,同时作为调用栈回溯 require 相对路径的键。
// 不同模块根出现同名文件时加根编号前缀,避免互相覆盖。
func (vm *VM) scriptName(origin fileOrigin) string {
	name := origin.file
	if v, ok := vm.origins.Load(name); ok {
		if prev := v.(fileOrigin); prev.root != origin.root {
			id := 0
			if origin.root != nil {
				id = origin.root.id
			}
			name = fmt.Sprintf("%d~%s", id, origin.file)
		}
	}
	vm.origins.Store(name, origin)
	return name
}

func (inst *instance) bindRequire() error {
	return inst.rt.Set("require", func(spec string) goja.Value {
		v, err := inst.loadModule(spec)
		if err != nil {
			panic(inst.rt.NewTypeError(err.Error()))
		}
		return v
	})
}

// importer 决定相对路径相对谁。加载期用帧栈(入口与模块执行中);
// 请求期帧栈已清空,则从 goja 调用栈找到定义当前函数的那个文件。
func (inst *instance) importer() fileOrigin {
	if n := len(inst.frames); n > 0 {
		return inst.frames[n-1]
	}
	for _, f := range inst.rt.CaptureCallStack(0, nil) {
		if v, ok := inst.vm.origins.Load(f.SrcName()); ok {
			return v.(fileOrigin)
		}
	}
	return fileOrigin{}
}

func (inst *instance) loadModule(spec string) (goja.Value, error) {
	root, file, err := inst.resolve(spec)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d\x00%s", root.id, file)
	if rec, ok := inst.modules[key]; ok {
		return rec.exports, nil
	}
	data, err := fs.ReadFile(root.fsys, file)
	if err != nil {
		return nil, fmt.Errorf("jsvm: read module %q: %w", file, err)
	}
	rec := &loadedModule{exports: goja.Undefined()}
	inst.modules[key] = rec

	if strings.HasSuffix(file, ".json") {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			delete(inst.modules, key)
			return nil, fmt.Errorf("jsvm: parse %s: %w", file, err)
		}
		rec.exports = inst.rt.ToValue(v)
		return rec.exports, nil
	}

	origin := fileOrigin{root: root, file: file}
	name := inst.vm.scriptName(origin)
	src := "(function(exports, require, module, __filename, __dirname) {\n" + string(data) + "\n})"
	prog, err := goja.Compile(name, src, false)
	if err != nil {
		delete(inst.modules, key)
		return nil, fmt.Errorf("jsvm: compile %s: %w", file, err)
	}
	fnVal, err := inst.rt.RunProgram(prog)
	if err != nil {
		delete(inst.modules, key)
		return nil, err
	}
	call, ok := goja.AssertFunction(fnVal)
	if !ok {
		delete(inst.modules, key)
		return nil, fmt.Errorf("jsvm: module %s did not evaluate to a function", file)
	}
	moduleObj := inst.rt.NewObject()
	exports := inst.rt.NewObject()
	_ = moduleObj.Set("exports", exports)
	rec.exports = exports // 循环依赖拿到的是已经赋值的部分

	dir := path.Dir(file)
	if dir == "." {
		dir = ""
	}
	inst.frames = append(inst.frames, origin)
	_, err = call(goja.Undefined(), exports, inst.rt.Get("require"), moduleObj, inst.rt.ToValue(file), inst.rt.ToValue(dir))
	inst.frames = inst.frames[:len(inst.frames)-1]
	if err != nil {
		delete(inst.modules, key)
		return nil, err
	}
	rec.exports = moduleObj.Get("exports")
	return rec.exports, nil
}

func (inst *instance) resolve(spec string) (*moduleRoot, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, "/") || strings.ContainsAny(spec, `\`) {
		return nil, "", fmt.Errorf("jsvm: invalid module path %q", spec)
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		imp := inst.importer()
		if imp.root == nil {
			where := imp.file
			if where == "" {
				where = "an inline script"
			}
			return nil, "", fmt.Errorf("jsvm: relative require %q is not available from %s", spec, where)
		}
		joined := path.Clean(path.Join(path.Dir(imp.file), spec))
		if !fs.ValidPath(joined) {
			return nil, "", fmt.Errorf("jsvm: module %q escapes its root", spec)
		}
		got, err := resolveFile(imp.root.fsys, joined)
		if err != nil {
			return nil, "", fmt.Errorf("jsvm: module %q not found", spec)
		}
		return imp.root, got, nil
	}
	cleaned := path.Clean(spec)
	if !fs.ValidPath(cleaned) {
		return nil, "", fmt.Errorf("jsvm: invalid module path %q", spec)
	}
	imp := inst.importer()
	if imp.root != nil {
		if got, err := resolveFile(imp.root.fsys, cleaned); err == nil {
			return imp.root, got, nil
		}
	}
	for _, root := range inst.vm.roots {
		if imp.root != nil && root == imp.root {
			continue
		}
		if got, err := resolveFile(root.fsys, cleaned); err == nil {
			return root, got, nil
		}
	}
	return nil, "", fmt.Errorf("jsvm: module %q not found", spec)
}

// resolveFile 按 Node 的习惯补全扩展名:精确路径、.js、.json、目录下的 index.js / index.json。
func resolveFile(fsys fs.FS, p string) (string, error) {
	cands := []string{p}
	if path.Ext(p) == "" {
		cands = []string{p + ".js", p + ".json", path.Join(p, "index.js"), path.Join(p, "index.json")}
	}
	for _, c := range cands {
		if c == "." || !fs.ValidPath(c) {
			continue
		}
		info, err := fs.Stat(fsys, c)
		if err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fs.ErrNotExist
}
