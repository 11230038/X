package library

import "backend/internal/authz"

// The account these tests act as, and the one that holds the rows they store.
// It is a number rather than a name because an account id is what the owner
// column holds, and the point of the constant is that the stored rows and the
// caller agree about it: a fixture that stored ownerless rows and then called as
// this account would fail every test here for a reason none of them is about.
const testOwnerID int64 = 7

func testCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID, Role: authz.RoleUser}
}

// testActor is testCaller with the name a request would have carried, which is
// what the operations that record something need and what the reads do not.
//
// The name is not testOwnerID's because those are different facts: an id is what
// a row is scoped by, and a name is a snapshot of what an account was called when
// somebody did something to a file (§2.6).
func testActor() Actor {
	return Actor{Caller: testCaller(), Name: "zhou"}
}

// otherCaller is a second account, for the one thing a single-account fixture
// cannot show: that a file belonging to somebody else is not there.
func otherCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 1, Role: authz.RoleUser}
}

func adminCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 2, Role: authz.RoleAdmin}
}
