package outbound

import "net/netip"

// ExternalMediaPolicy is the boundary for an address that came from outside:
// a media URL in a message on an IM channel, written by whoever sent the
// message.
//
// It allows the public internet and nothing else. Everything that is not
// globally routable is a destination the sender of a message has no business
// naming — the deployment's own machines, this host, the link it is on, and the
// metadata service that would hand over the credentials to run on it.
type ExternalMediaPolicy struct{}

func (ExternalMediaPolicy) Allow(addr netip.Addr) bool {
	return Classify(addr) == CategoryPublic
}

// LANServicePolicy is the boundary for an address the deployment's own operator
// wrote down: the LAN address of an MCP server, for the usual case where that
// server is on the same network as the product.
//
// It differs from ExternalMediaPolicy in exactly one way, and it is the whole
// reason there are two types: private space is allowed, because the operator
// said so about their own network. Loopback, the metadata service, link-local
// and the rest stay refused — an operator does not need to configure a service
// at the address that hands out credentials, and "it is on my LAN" is not a
// reason to reach this host's own loopback interface on its behalf.
type LANServicePolicy struct{}

func (LANServicePolicy) Allow(addr netip.Addr) bool {
	switch Classify(addr) {
	case CategoryPublic, CategoryPrivate:
		return true
	default:
		return false
	}
}

// Check answers whether every address behind one name may be reached.
//
// A name usually resolves to several addresses, and the answer is yes only if
// all of them are allowed: an attacker who controls a name they know will be
// resolved can point it at 8.8.8.8 and 127.0.0.1 at once, and a check that
// stopped at the first allowed address would pass the request on to a client
// that picks either one. So this is a loop rather than a lookup, and the error
// names the address that failed rather than the name.
//
// The resolution is not here. It happens in the caller, which is the only place
// it can happen — this package makes no requests — and it hands the answer over
// whole. A caller that resolved nothing gets ErrNoAddresses rather than a nil,
// because a name that did not resolve is not a name that resolved to something
// allowed.
func Check(policy Policy, addrs []netip.Addr) error {
	if policy == nil {
		return ErrNotConfigured
	}
	if len(addrs) == 0 {
		return ErrNoAddresses
	}
	for _, addr := range addrs {
		if !policy.Allow(addr) {
			return &Blocked{Address: addr, Category: Classify(addr)}
		}
	}
	return nil
}

// The two policies stated where the compiler checks them. Nothing in this
// repository calls either yet (§0.2 第 5 条), so without these lines a method
// renamed away would leave a type that no longer satisfies Policy and nothing
// to notice it — the same reason the type is asserted rather than described.
var (
	_ Policy = ExternalMediaPolicy{}
	_ Policy = LANServicePolicy{}
)
