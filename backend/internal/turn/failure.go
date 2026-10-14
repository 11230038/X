package turn

import (
	"context"
	"errors"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/usage"
)

// retryDecider is a failure that knows whether repeating it could ever work.
//
// Whether a call is worth repeating is sometimes the provider's call rather than
// this service's: a rejected key or a request it will not accept fails the same
// way every time. A client told to retry one of those has been told something
// untrue, and it will find out by failing again.
type retryDecider interface{ Retryable() bool }

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
	CodeQuotaExceeded:     "本账号的模型调用额度已用完，本轮没有开始",
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
	case errors.Is(failure, usage.ErrQuotaExceeded):
		// Retryable, and the client is told why the wait is a long one: the
		// refusal carries the item and the moment the period ends, and both
		// travel on the error event (§7).
		described.Code, described.Retryable = CodeQuotaExceeded, true
		var exceeded *usage.Exceeded
		if errors.As(failure, &exceeded) {
			described.Quota = exceeded
		}
	case errors.Is(failure, context.DeadlineExceeded):
		described.Code, described.Retryable = CodeTimeout, true
	}

	// A failure this service could not classify may still have come from
	// something that classified it itself, and that answer is better than the
	// default: a provider that rejected the request knows a retry will be
	// rejected the same way, whatever the text of the failure says.
	if described.Code == CodeInternalError {
		var decider retryDecider
		if errors.As(failure, &decider) {
			described.Retryable = decider.Retryable()
		}
	}
	return described
}
