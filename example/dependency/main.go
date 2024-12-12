package main

import (
	"context"
	"log"
	"time"

	"github.com/rushteam/beauty"
)

func main() {
	app := beauty.New()

	// 注册数据库服务
	app.RegisterDependency("database", nil, func() bool {
		return checkDBConnection()
	})

	// 注册依赖数据库的缓存服务
	app.RegisterDependency("cache", []string{"database"}, func() bool {
		return checkCacheConnection()
	})

	// 注册API服务，依赖缓存
	app.RegisterDependency("api", []string{"cache"}, func() bool {
		return true // API服务本身总是就绪
	})

	// 启动服务
	if err := app.Start(context.TODO()); err != nil {
		log.Fatal(err)
	}
}
func checkDBConnection() bool {
	time.Sleep(time.Second)
	return true
}
func checkCacheConnection() bool {
	time.Sleep(time.Second)
	return true
}
