package notebook

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// A question's identity is a content address: the question text and its type,
// hashed. It is what makes "the same question" a decidable thing.
//
// Nothing else can. An id the model invents is neither stable nor unique; an id
// the database hands out makes every generation a new row, which turns the
// upsert the question bank is built on back into an insert. The same reasoning
// produced V2's chunk ids, and the same one produces the category id below.
const (
	questionIDPrefix = "qn_"
	// questionIDHexBytes is how much of the digest an identity keeps. Twelve
	// bytes is short enough to read in a log and wide enough that a person
	// producing a few thousand questions will not meet a collision by accident.
	questionIDHexBytes = 12
	categoryIDPrefix   = "nbc_"
)

// normalize rewrites a question so that two questions differing only in
// whitespace get one identity.
//
// It does exactly two things: a run of whitespace becomes one space, and
// whitespace at either end is dropped. It does not fold case, and it does not
// fold full-width characters to half-width ones — an ideographic space is a
// character users type on purpose, and converting it is the 全角半角转换 this
// function refuses to do.
//
// The direction is deliberate: 宁可重复，不可误并. A missed merge costs a
// duplicate row, which the user can see and delete. A wrong merge silently
// replaces one question's text with another's, and nothing downstream can tell
// that it happened.
//
// Only ASCII whitespace counts, and that is the same decision: strings.Fields
// would treat U+3000 as a separator and so fold the one character the paragraph
// above says must survive.
func normalize(question string) string {
	var builder strings.Builder
	builder.Grow(len(question))
	pendingSpace := false
	for _, character := range question {
		if isASCIIWhitespace(character) {
			// At the front nothing has been written yet, so there is no space to
			// remember; at the end the remembered one is never followed by a
			// character, so it is never written. Trimming is what falls out of
			// this rather than a second pass over the string.
			pendingSpace = builder.Len() > 0
			continue
		}
		if pendingSpace {
			builder.WriteByte(' ')
			pendingSpace = false
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func isASCIIWhitespace(character rune) bool {
	switch character {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// QuestionID is the identity of a question: the content address of its text and
// its type.
//
// The type takes part, so the same sentence asked as a multiple-choice question
// and as a short-answer one is two questions. They are: they are answered
// differently, judged differently, and one of them has options.
func QuestionID(question string, questionType QuestionType) string {
	digest := sha256.New()
	digest.Write([]byte(normalize(question)))
	// A real NUL byte, not the two characters "\x00". Without a separator no
	// question can contain, the concatenation would make ("ab", "c") and
	// ("a", "bc") the same address — and the collision would never show up in a
	// test, only in the one question someone actually writes.
	digest.Write([]byte{0})
	digest.Write([]byte(questionType))
	return questionIDPrefix + hex.EncodeToString(digest.Sum(nil))[:questionIDHexBytes*2]
}

// categoryID is a category's identity. It is derived from the owner and the name
// rather than generated, so two writers of the same account creating the same
// category at the same moment compute the same key: the second insert collides
// on the primary key and is read as "already there" instead of becoming a
// duplicate row.
//
// The owner is part of the hash because the name alone is no longer a category's
// identity. Migration 00014 made a name unique per owner rather than globally —
// two accounts may each keep a "数学" — and a key derived from the name alone
// would defeat that: the second account's insert would collide on the primary
// key with the first account's row, and the collision-tolerant write would read
// that as "the category is already there", filing the second account's question
// under the first account's category and exposing the first account's label to
// the second. With the owner in the key the two are two rows, and the composite
// unique key the migration declared holds.
//
// A caller with no account — ownerID zero — keeps the name-only key, which is
// the key the pre-V5 rows were written under: "no owner" is a value, and it
// degenerates to the old rule exactly where the old data is.
//
// Categories are matched by name under a binary collation, so "Math" and "math"
// are two categories: the hash is taken over the name as written, which is the
// same rule. The owner and the name are separated by a NUL so that owner 1 with
// name "23" and owner 12 with name "3" do not meet at the same key.
func categoryID(ownerID int64, name string) string {
	digest := sha256.New()
	if ownerID > 0 {
		digest.Write([]byte(strconv.FormatInt(ownerID, 10)))
		digest.Write([]byte{0})
	}
	digest.Write([]byte(name))
	return categoryIDPrefix + hex.EncodeToString(digest.Sum(nil))[:questionIDHexBytes*2]
}
