package handler

// The refusal every HTTP surface answers with, written once.
//
// It is one function rather than a case in each handler's own switch because it
// is the same refusal wherever it happens. A route that would have made a model
// call is refused by the recorder before the call, and which route it was is of
// no interest to the client — two copies would be two chances to spell the code
// or the details differently, and a client branches on both.
//
// The two facts §7 asks a refusal to name travel in the details rather than in
// the sentence: which item ran out, and when it starts over. A sentence is for
// a person to read and the fields are for a client to draw from, which is the
// same division the stale-review refusal makes and the reason this is not one
// long message.

import (
	"errors"
	"net/http"

	"backend/internal/httpx"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

// quotaMessage is what a client that does not read the details is told.
const quotaMessage = "this account has used up its model quota for the period"

// writeQuotaRefusal answers a refusal and reports whether the error was one.
//
// The answer is 429 and it carries no Retry-After header, and the omission is
// deliberate: a header would be honoured automatically by any client that
// already knows the one for `rate_limited`, and the wait here is the end of a
// day or a month. That is something to explain to a user, not to sleep through.
// The moment is in the details instead, beside the item, which is what a client
// needs to draw where to look at the usage and when it may come back.
func writeQuotaRefusal(c *gin.Context, err error) bool {
	var exceeded *usage.Exceeded
	if !errors.As(err, &exceeded) {
		return false
	}
	httpx.WriteErrorDetails(c, http.StatusTooManyRequests, "quota_exceeded", quotaMessage, gin.H{
		"item":      exceeded.Item,
		"limit":     exceeded.Limit,
		"used":      exceeded.Used,
		"resets_at": exceeded.ResetsAt.UTC(),
	})
	return true
}
