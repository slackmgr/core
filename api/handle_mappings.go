package api

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
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil, "")
		return
	}

	c.Data(http.StatusOK, "application/json", data)
}
