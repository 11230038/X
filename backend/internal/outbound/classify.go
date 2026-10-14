package outbound

import "net/netip"

// Classify names what an address is.
//
// It is a pure function over netip.Addr and not over net.IP, and that is §1.1's
// point rather than a preference: net.IPNet.Contains does not understand that
// ::ffff:127.0.0.1 *is* 127.0.0.1, so it compares it against the IPv4 ranges as
// an ordinary IPv6 address, finds no match, and reports it public. That is the
// most common single way an SSRF is written, and it is a mistake a type can
// prevent: netip.Addr knows whether it is IPv4-mapped, and Unmap says so.
func Classify(addr netip.Addr) Category {
	if !addr.IsValid() {
		return CategoryInvalid
	}
	// A zone says which interface to leave by, not where to go, so it changes no
	// answer here — and leaving it on would change one for the worse: a prefix
	// does not contain a zoned address, so fe80::1%eth0 would fall through every
	// rule and come back public.
	addr = addr.WithZone("")
	// An IPv4 address wearing an IPv6 spelling is classified as the IPv4 address
	// it carries. There are two spellings: the mapped one (::ffff:0:0/96, RFC
	// 4291) and the translated one (the NAT64 prefixes of RFC 6052/8215). Both
	// say "go to this IPv4 host", and a classifier that read them as unrelated
	// IPv6 addresses would answer "public" for 127.0.0.1 twice over.
	if addr.Is4In6() {
		addr = addr.Unmap()
	} else if v4, ok := translated(addr); ok {
		addr = v4
	}

	for _, special := range specialRanges {
		if special.prefix.Contains(addr) {
			return special.category
		}
	}
	// Nothing matched, so the address is globally routable. See Category: this
	// is the deliberate default, and it is why the table below has to be the
	// registry rather than a list of the ranges somebody happened to think of.
	return CategoryPublic
}

// specialRange is one entry of the table: a range from IANA's special-purpose
// registries and the category it means.
type specialRange struct {
	prefix   netip.Prefix
	category Category
}

// specialRanges is the whole of the classifier, and it is a table because that
// is the only shape in which it can be read against the registry it came from.
//
// Order matters in one place and one place only: metadata is inside link-local,
// so its /32 is written before the /16 that contains it. Everything else is
// disjoint, and the IPv4 and IPv6 entries cannot collide — a prefix contains an
// address only if the two are the same family.
//
// What is deliberately not here: 6to4 (2002::/16) and Teredo (2001::/32, inside
// the 2001::/23 entry below) carry an IPv4 address inside an IPv6 one, and this
// classifier does not dig it out. It refuses both blocks instead of unwrapping
// them, which is the honest answer — reaching an IPv4 host through either needs
// a relay that has been deprecated for years, and a classifier that claimed to
// unwrap them would be claiming more than it checks.
var specialRanges = []specialRange{
	// The metadata service, first because it is inside link-local. 169.254.169.254
	// is the address every major cloud answers on; the ranges that hold the
	// others (100.64/10 for one, 169.254/16 for the rest) are refused below
	// anyway, so this entry exists to name the target rather than to catch it.
	{netip.MustParsePrefix("169.254.169.254/32"), CategoryMetadata},

	// This machine.
	{netip.MustParsePrefix("127.0.0.0/8"), CategoryLoopback},
	{netip.MustParsePrefix("::1/128"), CategoryLoopback},

	// One link and no further.
	{netip.MustParsePrefix("169.254.0.0/16"), CategoryLinkLocal},
	{netip.MustParsePrefix("fe80::/10"), CategoryLinkLocal},

	// The two address spaces a LAN is made of: RFC1918 and RFC4193 unique local.
	{netip.MustParsePrefix("10.0.0.0/8"), CategoryPrivate},
	{netip.MustParsePrefix("172.16.0.0/12"), CategoryPrivate},
	{netip.MustParsePrefix("192.168.0.0/16"), CategoryPrivate},
	{netip.MustParsePrefix("fc00::/7"), CategoryPrivate},

	// Carrier-grade NAT, which is also where one vendor put its metadata service.
	{netip.MustParsePrefix("100.64.0.0/10"), CategoryShared},

	// Everything else that is allocated but not a destination.
	{netip.MustParsePrefix("0.0.0.0/8"), CategoryReserved},       // "this network"
	{netip.MustParsePrefix("192.0.0.0/24"), CategoryReserved},    // IETF protocol assignments
	{netip.MustParsePrefix("192.0.2.0/24"), CategoryReserved},    // TEST-NET-1
	{netip.MustParsePrefix("192.88.99.0/24"), CategoryReserved},  // 6to4 relay anycast, deprecated
	{netip.MustParsePrefix("198.18.0.0/15"), CategoryReserved},   // benchmarking
	{netip.MustParsePrefix("198.51.100.0/24"), CategoryReserved}, // TEST-NET-2
	{netip.MustParsePrefix("203.0.113.0/24"), CategoryReserved},  // TEST-NET-3
	{netip.MustParsePrefix("224.0.0.0/4"), CategoryReserved},     // multicast, including 239/8
	{netip.MustParsePrefix("240.0.0.0/4"), CategoryReserved},     // reserved, including the broadcast
	// The unspecified address and the deprecated IPv4-compatible ones. This is
	// ::/96 and not ::/128 because "::0.0.0.2" is a spelling of an IPv4 host
	// that RFC 4291 retired and nothing stops a URL from using; ::1 is inside
	// it as well and is named loopback above, which is why the looping address
	// is listed before this block rather than in it.
	{netip.MustParsePrefix("::/96"), CategoryReserved},
	{netip.MustParsePrefix("100::/64"), CategoryReserved}, // discard-only
	// Site-local, retired by RFC 3879 and still written on old equipment: it is
	// a LAN's address under a name the current registry no longer hands out,
	// which is the shape of an address that gets forgotten.
	{netip.MustParsePrefix("fec0::/10"), CategoryReserved},
	{netip.MustParsePrefix("2001::/23"), CategoryReserved},     // IETF protocol assignments, Teredo among them
	{netip.MustParsePrefix("2001:db8::/32"), CategoryReserved}, // documentation
	{netip.MustParsePrefix("2002::/16"), CategoryReserved},     // 6to4, deprecated
	{netip.MustParsePrefix("3fff::/20"), CategoryReserved},     // documentation
	{netip.MustParsePrefix("ff00::/8"), CategoryReserved},      // multicast
}

// translated digs the IPv4 address out of the NAT64 prefixes, where RFC 6052
// puts it in the last four bytes.
//
// Both prefixes are here: the well-known one, which an IPv6-only network
// translates through, and the local-use one from RFC 8215. On a network running
// NAT64 — a mobile carrier's, typically — 64:ff9b::7f00:1 is an ordinary way to
// write 127.0.0.1, and it is a way that a check written before IPv6 was
// everywhere does not see.
func translated(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is6() {
		return netip.Addr{}, false
	}
	for _, prefix := range nat64Prefixes {
		if !prefix.Contains(addr) {
			continue
		}
		bytes := addr.As16()
		return netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]}), true
	}
	return netip.Addr{}, false
}

var nat64Prefixes = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),   // RFC 6052 well-known prefix
	netip.MustParsePrefix("64:ff9b:1::/48"), // RFC 8215 local-use prefix
}
