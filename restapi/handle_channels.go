package restapi

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
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil)
		return
	}

	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)

	if _, err = c.Writer.Write(data); err != nil {
		s.logger.Errorf("Failed to write channel list response: %s", err)
	}
}
