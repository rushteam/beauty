package biz

import (
	"github.com/gin-gonic/gin"
)

//Index .
func Index(ctx *gin.Context) {
	ctx.HTML(200, "index.html", nil)
}
