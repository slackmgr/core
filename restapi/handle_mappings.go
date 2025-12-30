package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleMappings(c *gin.Context) {
	data, err := json.MarshalIndent(s.apiSettings.RoutingRules, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to json marshal API routing rules: %w", err)
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil)
		return
	}

	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)

	if _, err = c.Writer.Write(data); err != nil {
		s.logger.Errorf("Failed to write API routing rules response: %s", err)
	}
}
