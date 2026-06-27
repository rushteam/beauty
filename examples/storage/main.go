// storage 示例:版本化对象存储,OCC 乐观锁。
//
// 演示 pkg/domain/storage:Write IfNotExist/IfMatch/LastWriteWins 三种写语义、
// 版本冲突检测、Read 权限、批量原子写。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/storage"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	s := storage.New()

	mux := http.NewServeMux()

	// /save?user=u1&key=slot1&value=hello  写入(IfNotExist)。
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o, err := s.Write(storage.WriteOp{
			OwnerID: q.Get("user"), Collection: "save", Key: q.Get("key"),
			Value: []byte(q.Get("value")), Mode: storage.WriteIfNotExist,
			ReadAccess: 0, WriteAccess: 1,
		}, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"version": o.Version, "key": q.Get("key")})
	})

	// /update?user=u1&key=slot1&value=world&version=xxx  OCC 更新。
	mux.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o, err := s.Write(storage.WriteOp{
			OwnerID: q.Get("user"), Collection: "save", Key: q.Get("key"),
			Value: []byte(q.Get("value")), Mode: storage.WriteIfMatch, Version: q.Get("version"),
			WriteAccess: 1,
		}, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"version": o.Version})
	})

	// /load?user=u1&key=slot1  读取。
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o, _ := s.Read(q.Get("user"), "save", q.Get("key"), q.Get("user"))
		if o == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"value": string(o.Value), "version": o.Version, "updated": o.UpdateTime,
		})
	})

	// /count  对象总数。
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("count=" + strconv.Itoa(s.Count())))
	})

	app := beauty.New(beauty.WithWebServer(":8293", mux, webserver.WithServiceName("storage-demo")))
	println("storage demo on :8293")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
