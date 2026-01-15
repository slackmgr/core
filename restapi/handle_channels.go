package restapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleChannels(c *gin.Context) {
	channels := s.channelInfoProvider.ManagedChannels()
	c.IndentedJSON(http.StatusOK, channels)
}
