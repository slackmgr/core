package restapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) ping(c *gin.Context) {
	// In verbose mode, support a "panic" query parameter to trigger a panic for testing recovery middleware.
	if s.cfg.Verbose {
		shouldPanic := c.Query("panic") == "true"

		if shouldPanic {
			panic("ping pong panic")
		}
	}

	statusCode := http.StatusOK

	// Optionally set status code as requested via the "status" query parameter.
	if status := c.Query("status"); status != "" {
		if s, err := strconv.Atoi(status); err == nil && s >= 100 && s <= 599 {
			statusCode = s
		}
	}

	c.Status(statusCode)

	c.Header("Content-Type", "text/plain")

	if _, err := c.Writer.Write([]byte("pong")); err != nil {
		s.logger.Errorf("Failed to write ping response: %s", err)
	}
}
