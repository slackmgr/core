package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleChannels(c *gin.Context) {
	channels := s.channelInfoSyncer.ManagedChannels()

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal channel list: %w", err)
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil, "")
		return
	}

	c.Data(http.StatusOK, "application/json", data)
}
