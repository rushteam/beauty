package conf

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// LoadDotEnv 加载 .env 文件中的变量到进程环境（不覆盖已存在的环境变量）。
// 未指定路径时默认加载工作目录下的 .env 文件。
// 加载后 ${env:VAR} 占位符和 os.Getenv 均可访问这些变量。
//
//	conf.LoadDotEnv()                     // 加载 .env
//	conf.LoadDotEnv(".env", ".env.local") // 按顺序加载，后者优先
func LoadDotEnv(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	return godotenv.Load(paths...)
}

// OverloadDotEnv 与 LoadDotEnv 类似，但会覆盖已存在的同名环境变量。
// 适用于本地开发时 .env.local 需要强制覆盖容器/系统级变量的场景。
func OverloadDotEnv(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	return godotenv.Overload(paths...)
}

// DotEnvProvider 返回一个 Provider，从 .env 文件解析键值对来解析
// ${dotenv:VAR} 占位符，不污染进程环境。
// 未指定路径时默认读取 .env。
//
//	loader = conf.WithSecrets(loader, conf.EnvProvider(), conf.DotEnvProvider())
func DotEnvProvider(paths ...string) Provider {
	return &dotenvProvider{paths: paths}
}

type dotenvProvider struct {
	paths  []string
	values map[string]string
	loaded bool
	err    error
}

func (p *dotenvProvider) Scheme() string { return "dotenv" }

func (p *dotenvProvider) load() {
	if p.loaded {
		return
	}
	p.loaded = true
	files := p.paths
	if len(files) == 0 {
		files = []string{".env"}
	}
	merged := make(map[string]string)
	for _, f := range files {
		m, err := godotenv.Read(f)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			p.err = fmt.Errorf("conf: read dotenv %q: %w", f, err)
			return
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	p.values = merged
}

func (p *dotenvProvider) Get(_ context.Context, key string) (string, error) {
	p.load()
	if p.err != nil {
		return "", p.err
	}
	v, ok := p.values[key]
	if !ok {
		return "", fmt.Errorf("conf: dotenv key %q not found", key)
	}
	return v, nil
}
