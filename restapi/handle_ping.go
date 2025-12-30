package restapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) ping(c *gin.Context) {
	shouldPanic := c.Query("panic") == "true"

	if shouldPanic {
		panic("ping pong panic")
	}

	statusCode := http.StatusOK

	if status := c.Query("status"); status != "" {
		if s, err := strconv.Atoi(status); err == nil && s >= 100 && s <= 599 {
			statusCode = s
		}
	}

	// Set the status code as requested, default 200 OK.
	c.Status(statusCode)

	c.Header("Content-Type", "text/plain")

	if _, err := c.Writer.Write([]byte("pong")); err != nil {
		s.logger.Errorf("Failed to write ping response: %s", err)
	}
}
