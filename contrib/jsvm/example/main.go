// 示例:用 JS 脚本扩展一个 beauty 应用。
//
//	cd contrib/jsvm && go run ./example
//	curl -X PUT localhost:8080/notes/hello.txt -d 'hi'
//	curl localhost:8080/notes/hello.txt
//	curl -X POST localhost:8080/orders -d '{"amount": 1200}'
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/jsvm"
	"github.com/rushteam/beauty/pkg/store/blob"
)

//go:embed hooks/*.js
var hooks embed.FS

func main() {
	bucket, err := blob.NewLocal(os.TempDir() + "/beauty-jsvm-example")
	if err != nil {
		log.Fatal(err)
	}
	defer bucket.Close()

	// 只把 main.js 当入口;pricing.js 由 main.js require,不会被重复执行。
	vm, err := jsvm.New(jsvm.WithFS(hooks, "hooks/main.js"), jsvm.WithBucket(bucket))
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", vm)
	// Go 侧业务定义扩展点 "order.create",定价规则交给脚本。
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		order := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := vm.Trigger(r.Context(), "order.create", order); err != nil {
			var je *jsvm.Error
			if errors.As(err, &je) && je.Status > 0 {
				http.Error(w, je.Message, je.Status)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(order)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := beauty.New(beauty.WithService(vm), beauty.WithWebServer(":8080", mux))
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
}
