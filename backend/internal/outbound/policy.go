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
// wrote down: the document parser, which is a service beside the product rather
// than out on the internet.
//
// It differs from ExternalMediaPolicy in exactly one way, and it is the whole
// reason there are two types: the operator's own network is allowed, because the
// operator said so. That is private space and it is also this host — the parser
// this build ships against defaults to `http://127.0.0.1:5001`, so a policy that
// refused loopback would refuse the deployment's own default configuration,
// which is a boundary that has stopped describing what it is for.
//
// "The operator's own network" is the whole of the widening, and it stops where
// it should: link-local, the metadata service, the reserved blocks and the rest
// stay refused. An operator does not need to configure a service at the address
// that hands out credentials to run on this host, and "it is mine" is not a
// reason to reach it.
type LANServicePolicy struct{}

func (LANServicePolicy) Allow(addr netip.Addr) bool {
	switch Classify(addr) {
	case CategoryPublic, CategoryPrivate, CategoryLoopback:
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

// The two policies stated where the compiler checks them. Both now have a
// caller — dialer.go turns one into the dial hook of the three clients that
// leave this machine — so this is a second net rather than the only one: a
// method renamed away fails to compile where it is passed, and these lines are
// what would catch the case where it is not passed at all.
var (
	_ Policy = ExternalMediaPolicy{}
	_ Policy = LANServicePolicy{}
)
