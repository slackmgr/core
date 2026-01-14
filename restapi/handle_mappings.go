package restapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleMappings(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, s.apiSettings.RoutingRules)
}
