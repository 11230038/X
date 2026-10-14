package data

import "gorm.io/gorm"

// OwnerScope narrows a query to the rows one account may see.
//
// Migration 00014 gave eight tables an owner_id, and every read over them goes
// through here, because the rule has three cases and each is easy to get wrong
// on its own:
//
//   - a row the caller owns;
//   - a row that belongs to nobody, which is what every row written before V5
//     looks like — visible to the administrator and to nobody else, because
//     somebody has to be able to hand them out;
//   - anything else.
//
// Writing it once is the whole point. A read that forgets it does not fail: it
// lists another account's rows, and a list with too much in it looks exactly
// like a list that works. That is why §7.2 asks for a two-account test on every
// listing rather than on one of them.
//
// column is a parameter rather than the constant "owner_id" so that a query
// joining two owned tables can say which one it means; the parenthesis is not
// decoration, because GORM ANDs this against the rest of the WHERE clause and
// without it the `IS NULL` would escape it.
func OwnerScope(query *gorm.DB, ownerID int64, admin bool, column string) *gorm.DB {
	if admin {
		return query.Where("("+column+" = ? OR "+column+" IS NULL)", ownerID)
	}
	return query.Where(column+" = ?", ownerID)
}

// OwnerPointer is the owner column's value as a write wants it.
//
// The column is nullable because it has to be: migration 00014 could not invent
// an owner for the rows that were already there, and 00014 is the whole of what
// V5 changes about them. So "no account" is a value the column takes, and the
// only zero it can hold is NULL — a row written with owner_id 0 is one that
// OwnerScope's `= ?` matches for nobody, which is a row that exists and cannot
// be read back, not a row that belongs to no one.
//
// It lives beside OwnerScope because the two are one decision seen from the two
// ends of a round trip: writes go through here, reads through there, and a
// change to what "no owner" means has to happen in both.
func OwnerPointer(ownerID int64) *int64 {
	if ownerID <= 0 {
		return nil
	}
	return &ownerID
}
