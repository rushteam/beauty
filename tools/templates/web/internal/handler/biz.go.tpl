package biz

import (
	"github.com/gin-gonic/gin"
	"{{.ModPath}}internal/handler"
	"{{.ModPath}}internal/types"
)

//Index .
func Index(ctx *gin.Context) {
	ctx.JSON(200, types.Resp{
		Errno:  0,
		Errmsg: "ok",
	})
}
