package server

// The double behind the four account-lifecycle routes.
//
// It is a file of its own because router_stubs_test.go is already the longest
// stub file in this package: what these four need is not more answers, only the
// fact that the routes are mounted and reachable. Every method accepts and
// answers nothing interesting on purpose — what a deletion really removes or
// what a reset really revokes is internal/auth's and handler/'s business, and a
// stub that decided it here would let this package agree with itself about a
// rule it does not own.

import (
	"context"

	"backend/internal/auth"
)

type routerAccountLifecycleStub struct{}

// CreateAccount answers with the account it was asked to make, under an id no
// case names, so that a route wired to the wrong handler cannot produce this
// body by accident.
func (routerAccountLifecycleStub) CreateAccount(
	_ context.Context, input auth.CreateAccountInput,
) (auth.User, error) {
	return auth.User{UserID: 99, Username: input.Username, Role: input.Role}, nil
}

func (routerAccountLifecycleStub) DeleteAccount(context.Context, int64) error { return nil }

func (routerAccountLifecycleStub) ResetPassword(context.Context, int64, string) error { return nil }

func (routerAccountLifecycleStub) SetDisabled(context.Context, int64, bool) error { return nil }
