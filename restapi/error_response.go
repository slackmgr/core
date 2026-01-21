package restapi

// errorResponse is the JSON structure returned for all error responses.
type errorResponse struct {
	Error string `json:"error"`
}
