package restapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/core/internal"
)

func (s *Server) handleChannels(c *gin.Context) {
	channels := s.channelInfoProvider.ManagedChannels()

	// Return an empty array rather than null before the channel list is loaded.
	if channels == nil {
		channels = []*internal.ChannelSummary{}
	}

	c.IndentedJSON(http.StatusOK, channels)
}
