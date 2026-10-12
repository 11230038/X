package turn

import (
	"context"
	"errors"

	"backend/internal/agentloop"
	"backend/internal/capability"
)

// clientMessages is what a client is told about each failure code.
//
// The internal reason stays in the log and the database row: a client acts on
// the code, and a provider's own error text tells it nothing it can use while
// telling whoever reads it more about the deployment than it should.
var clientMessages = map[string]string{
	CodeUnknownCapability: "这个会话请求了本服务没有的能力",
	CodeInputTimeout:      "等待回复超过时限，本轮已结束",
	CodeRoundsExhausted:   "模型反复调用工具，已达到上限",
	CodeTimeout:           "模型响应超时",
	CodeInternalError:     "服务内部错误，请稍后再试",
}

// describe turns a failure into what an operator and a client each need.
func describe(failure error) Failure {
	described := Failure{Error: failure.Error(), Code: CodeInternalError, Retryable: true}
	switch {
	case errors.Is(failure, capability.ErrUnknownCapability):
		described.Code, described.Retryable = CodeUnknownCapability, false
	case errors.Is(failure, ErrInputTimeout):
		described.Code, described.Retryable = CodeInputTimeout, true
	case errors.Is(failure, agentloop.ErrRoundsExhausted):
		described.Code, described.Retryable = CodeRoundsExhausted, false
	case errors.Is(failure, context.DeadlineExceeded):
		described.Code, described.Retryable = CodeTimeout, true
	}
	return described
}
