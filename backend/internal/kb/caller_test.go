package kb

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

// otherCaller is a second account, for the one thing a single-account fixture
// cannot show: that a collection belonging to somebody else is not there.
func otherCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 1, Role: authz.RoleUser}
}

func adminCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 2, Role: authz.RoleAdmin}
}
