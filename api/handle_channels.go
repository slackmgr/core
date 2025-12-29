package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleChannels(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	channels := s.channelInfoSyncer.ManagedChannels()

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal channel list: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	if _, err = resp.Write(data); err != nil {
		s.logger.Errorf("Failed to write channel list response: %s", err)
	}

	s.metrics.Observe(httpRequestMetric, time.Since(started).Seconds(), req.URL.Path, req.Method, strconv.Itoa(http.StatusOK))
}
