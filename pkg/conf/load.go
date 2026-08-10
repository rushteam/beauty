package conf

import (
	"errors"
	"os"
)

// configFileEnv 是 FileFromEnv 读取的环境变量名。
const configFileEnv = "CONFIG_FILE"

// FileFromEnv 从 CONFIG_FILE 环境变量获取配置文件路径，
// 未设置时返回 fallback。适合容器化部署统一注入配置路径。
//
//	path := conf.FileFromEnv("config/app.yaml")
func FileFromEnv(fallback string) string {
	if v := os.Getenv(configFileEnv); v != "" {
		return v
	}
	return fallback
}

// Load 是面向业务代码的一站式配置加载快捷方式：
//
//   - path 为空 → 跳过（返回 nil）
//   - 文件不存在 → 跳过（返回 nil）
//   - 文件存在 → New + WithSecrets + Unmarshal
//
// 典型用法：
//
//	var cfg AppConfig
//	if err := conf.Load(conf.FileFromEnv("config/app.yaml"), &cfg); err != nil {
//	    log.Fatal(err)
//	}
func Load(path string, dst any) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	loader, err := New(path)
	if err != nil {
		return err
	}
	return WithSecrets(loader).Unmarshal(dst)
}
