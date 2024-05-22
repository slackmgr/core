package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) mappings(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	data, err := json.MarshalIndent(s.alertMapping, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal alert mappings: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	if _, err = resp.Write(data); err != nil {
		s.logger.Errorf("Failed to write mappings response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}
