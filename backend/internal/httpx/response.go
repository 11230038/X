package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const requestIDKey = "request_id"

// ErrorResponse is the stable JSON shape returned for client and server errors.
type ErrorResponse struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

// ErrorBody describes an error without exposing internal implementation details.
type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// RequestID returns the ID attached to the current request, if any.
func RequestID(c *gin.Context) string {
	value, _ := c.Get(requestIDKey)
	requestID, _ := value.(string)
	return requestID
}

// WriteError writes the common error envelope and aborts the request.
func WriteError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error:     ErrorBody{Code: code, Message: message},
		RequestID: RequestID(c),
	})
}

// WriteMethodNotAllowed handles routes that exist but do not support a method.
func WriteMethodNotAllowed(c *gin.Context) {
	WriteError(c, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

// WriteNotFound handles routes that are not registered.
func WriteNotFound(c *gin.Context) {
	WriteError(c, http.StatusNotFound, "not_found", "route not found")
}
