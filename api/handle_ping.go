package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) ping(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}
