// Package capability defines the capability protocol: what a capability
// declares about itself, how capabilities are registered, and the checks that
// stop a process whose declarations disagree with what it can actually run.
//
// It holds the protocol only. A concrete capability lives in its own package,
// so that registering one never means editing this one.
package capability

import "errors"

// ErrInvalidManifest reports a manifest that could not be registered.
var ErrInvalidManifest = errors.New("capability: invalid manifest")

// ErrDuplicateCapability reports a name that is already registered.
var ErrDuplicateCapability = errors.New("capability: duplicate capability")

// ErrUnknownCapability reports a name that is not registered.
var ErrUnknownCapability = errors.New("capability: unknown capability")

// ErrToolDrift reports a manifest tool the tool registry does not offer.
var ErrToolDrift = errors.New("capability: manifest references an unregistered tool")

// ErrConfigDrift reports capability tuning that no manifest can satisfy.
var ErrConfigDrift = errors.New("capability: configuration does not match a manifest")

// Config is the capability tuning read from the environment, keyed by
// capability name and listing the stages each configures.
//
// It is plain data rather than the config package's own type so that this
// package does not depend on how the environment is shaped.
type Config map[string][]string
