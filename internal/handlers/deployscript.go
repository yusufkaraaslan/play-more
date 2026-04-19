package handlers

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed playmore-deploy.sh
var deployScriptContent string

func ServeDeployScript(c *gin.Context) {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=playmore-deploy")
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, deployScriptContent)
}
