package api

import (
	"net/http"
	"time"
)

func (s *Server) ping(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	resp.Header().Add("Content-Type", "text/plain")
	resp.WriteHeader(http.StatusOK)

	if _, err := resp.Write([]byte("pong")); err != nil {
		s.logger.Errorf("Failed to write ping response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}
