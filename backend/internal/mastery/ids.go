package mastery

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// The prefixes of the row ids this package makes. They are readable on purpose:
// a path id turns up in `notebook_entries.mastery_path_id` and in a URL, and a
// knowledge point id turns up inside a state document that somebody will
// eventually read by eye.
//
// They are kept apart from the service that uses them because an id is the one
// thing here that has no rules and no caller: it is a name, made the same way
// for every caller, and grouping the names together keeps the service file about
// what the domain decides rather than about how a string is spelled.
const (
	pathIDPrefix           = "mp_"
	moduleIDPrefix         = "m_"
	knowledgePointIDPrefix = "kp_"
	eventIDPrefix          = "mev_"
	evidenceIDPrefix       = "mle_"
	interactionIDPrefix    = "min_"
	idRandomBytes          = 16
)

// NewPathID returns an id for a path that does not exist yet.
//
// It is exported because the capability makes the id before it can call Create:
// the turn that creates a path announces the id in its first event, and an id
// invented twice would be two paths.
func NewPathID() (string, error) {
	return newID(pathIDPrefix)
}

func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value), nil
}
