// dberr 示例:把数据库错误翻译为带业务码的 *Status。
//
// 演示 pkg/dberr:自定义 Driver.Classify(按哨兵 error 归类)、
// Translate 产出 *pkg/errors.Status、WithMapping 覆盖默认映射。
// 模拟仓储层抛 driver error,网关层拿到带 Code 的错误。
package main

import (
	"context"
	stderrors "errors"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/dberr"
	perr "github.com/rushteam/beauty/pkg/errors"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// 模拟驱动导出的哨兵 error。
var (
	errUnique   = stderrors.New("duplicate key value")
	errNotFound = stderrors.New("no rows in result set")
)

// demoDriver 把哨兵 error 归类为 ErrClass。
type demoDriver struct{}

func (demoDriver) Classify(err error) dberr.ErrClass {
	switch {
	case stderrors.Is(err, errUnique):
		return dberr.ClassUniqueViolation
	case stderrors.Is(err, errNotFound):
		return dberr.ClassNotFound
	}
	return dberr.ClassUnknown
}

// mockRepo 模拟仓储:按 query 返回不同 driver error。
func mockRepo(q string) error {
	switch q {
	case "dup":
		return errUnique
	case "miss":
		return errNotFound
	default:
		return stderrors.New("unknown driver error")
	}
}

func main() {
	tr := dberr.New(
		dberr.WithDriver(demoDriver{}),
		// 演示覆盖:把 UniqueViolation 改映射到 InvalidArgument。
		dberr.WithMapping(dberr.ClassUniqueViolation, perr.CodeInvalidArgument),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if err := mockRepo(r.URL.Query().Get("q")); err != nil {
			// 仓储只抛 driver error,这里统一翻译成 *Status。
			s := tr.Translate(err)
			perr.WriteHTTP(w, s)
			return
		}
		w.Write([]byte("ok"))
	})

	app := beauty.New(beauty.WithWebServer(":8296", mux, webserver.WithServiceName("dberr-demo")))
	println("dberr demo on :8296")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
