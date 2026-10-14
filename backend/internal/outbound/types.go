// Package outbound is the boundary on where this deployment may send a request
// (§2.12).
//
// The rule it exists for is not "private addresses are dangerous". It is that
// two things hand us an address, and they are not the same thing:
//
//   - a media URL inside a message on an IM channel was written by whoever sent
//     the message, and the only safe answer to a private address there is no;
//   - an MCP server's address was written by the deployment's own operator, and
//     those servers are usually on the LAN.
//
// The same 192.168.1.10 must be refused in the first case and allowed in the
// second, so what is checked is a (URL, origin) pair rather than a URL. §13.1
// 第 7 条 forbids merging the two into one permissive implementation, and the
// way that merge actually happens is a boolean parameter — Allow(addr,
// allowPrivate) — because a parameter is a thing a caller can pass wrongly. So
// the two policies below are two types, and there is no combined function to
// call by mistake.
//
// There is also no caller. The two scenarios above are a media URL for a
// channel this product does not have and an MCP server it does not run (§0.2
// 第 5 条), so nothing in this repository reaches this package yet. That does
// not change what a correct policy is, but it changes how one can be shown to
// be correct: by its tests, and by nothing else. A security check with no
// caller guards nothing today, and this file says so rather than implying
// otherwise.
//
// The package is pure. It classifies addresses and answers questions about
// them; it does not resolve names, parse URLs, or make requests. Resolution is
// I/O and belongs to the caller, which then hands the whole answer to Check.
package outbound

import (
	"errors"
	"fmt"
	"net/netip"
)

// Category is what an address is, in the terms the two policies are written in.
//
// The names follow IANA's registry rather than our own feelings about addresses,
// because the registry is the part that changes: the set of ranges that are not
// globally routable is finite and enumerated, and an address in none of them is
// public. What that means for a range nobody has thought of yet is that it is
// treated as public — which is the right default for a classifier whose job is
// to name, and the reason the policies that refuse things ask for their names
// explicitly.
type Category string

const (
	// CategoryPublic is globally routable, or unallocated-but-not-special. It is
	// the only category that is not a reason to refuse anything.
	CategoryPublic Category = "public"
	// CategoryPrivate is the address space a LAN uses: RFC1918 (10/8, 172.16/12,
	// 192.168/16) and its IPv6 counterpart, RFC4193 unique local (fc00::/7).
	// These two are one category because they answer one question — "could this
	// be the operator's own network" — and §2.12 turns on exactly that question.
	CategoryPrivate Category = "private"
	// CategoryLoopback is this machine: 127/8 and ::1.
	CategoryLoopback Category = "loopback"
	// CategoryLinkLocal is one link and no further: 169.254/16 and fe80::/10.
	CategoryLinkLocal Category = "link_local"
	// CategoryMetadata is the cloud instance metadata service, which is a link-local
	// address with a name of its own because it is the target that matters: it
	// hands out credentials to anything that can reach it, and it is the first
	// thing an SSRF looks for.
	CategoryMetadata Category = "metadata"
	// CategoryShared is 100.64/10, the carrier-grade NAT range. It is not
	// RFC1918 and not routable either, and it holds at least one metadata
	// service (100.100.100.200), so it is named rather than filed under reserved.
	CategoryShared Category = "shared"
	// CategoryReserved is everything else that is not a destination: multicast,
	// the test and documentation ranges, benchmarking, deprecated transition
	// blocks.
	CategoryReserved Category = "reserved"
	// CategoryInvalid is not an address at all — the zero netip.Addr, or one that
	// failed to parse. It is a category rather than an error because the answer
	// is the same as for every other category the policies refuse: no.
	CategoryInvalid Category = "invalid"
)

// Policy answers whether one address may be reached.
//
// It takes an address rather than a URL because an address is the whole of what
// a policy looks at, and it is one address at a time because a name may resolve
// to several — see Check for that case.
type Policy interface {
	Allow(netip.Addr) bool
}

// ErrBlocked is the identity of every refusal this package makes, for a caller
// that does not need to know which address it was.
var ErrBlocked = errors.New("outbound: that address may not be reached")

// ErrNoAddresses is what Check answers when it was handed nothing to check.
//
// A name that did not resolve is not a name that resolved to something allowed,
// and a policy check is the wrong place to discover the difference accidentally.
// It is a separate error rather than an ErrBlocked because nothing was refused:
// there was nothing there.
var ErrNoAddresses = errors.New("outbound: there is no address to check")

// ErrNotConfigured is what Check answers when it was handed no policy. A
// boundary that was never wired up must not be a boundary that lets everything
// through.
var ErrNotConfigured = errors.New("outbound: no policy was given")

// Blocked is a refusal, and it carries the two facts somebody reading a log
// needs: which address, and what it is.
//
// The category is here rather than in the sentence alone because the interesting
// case is a deployment debugging an operator's own config — "192.168.1.10 is
// private" is a fact about their network, while "outbound: that address may not
// be reached" is a fact about our code.
type Blocked struct {
	Address  netip.Addr
	Category Category
}

func (b *Blocked) Error() string {
	if b == nil {
		return ErrBlocked.Error()
	}
	return fmt.Sprintf("%s: %s is %s", ErrBlocked, b.Address, b.Category)
}

// Unwrap makes errors.Is(err, ErrBlocked) true for a refusal, so that a caller
// checking "was this the boundary" does not have to name the type.
func (b *Blocked) Unwrap() error { return ErrBlocked }
