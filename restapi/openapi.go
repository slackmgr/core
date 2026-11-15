package restapi

import (
	"bytes"
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// openAPIContentType is the media type of the OpenAPI document (RFC 9512).
const openAPIContentType = "application/yaml"

//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec returns the OpenAPI 3.1 specification of the REST API served by [Server], in YAML format.
// The [Server] also serves it at GET /openapi.yaml. The returned slice is a copy, which the caller may modify.
func OpenAPISpec() []byte {
	return bytes.Clone(openAPISpec)
}

// handleOpenAPISpec serves the embedded OpenAPI specification.
func (s *Server) handleOpenAPISpec(c *gin.Context) {
	c.Data(http.StatusOK, openAPIContentType, openAPISpec)
}
