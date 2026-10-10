package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const requestIDKey = "request_id"

// SuccessResponse is the stable JSON shape returned for successful requests.
// Together with ErrorResponse it forms one envelope: every response carries
// request_id and exactly one of data or error.
type SuccessResponse struct {
	Data      interface{} `json:"data"`
	RequestID string      `json:"request_id,omitempty"`
}

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

// WriteSuccess writes the common success envelope. Handlers pass only their own
// payload; the request ID is added here so no handler can forget or duplicate it.
func WriteSuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, SuccessResponse{Data: data, RequestID: RequestID(c)})
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
