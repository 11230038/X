package outbound

import (
	"net/netip"
	"testing"
)

// TestClassifyNamesTheAddress covers the whole table, one address at a time.
//
// The entries on the boundaries are the ones worth writing down: 172.15 and
// 172.32 either side of RFC1918's /12, 100.63 and 100.128 either side of the
// carrier-grade range, 223 and 224 either side of multicast. A table of ranges
// written by hand is exactly where an off-by-one-bit lives, and it is a mistake
// that shows up as a request reaching somewhere nobody meant it to.
func TestClassifyNamesTheAddress(t *testing.T) {
	tests := []struct {
		address  string
		category Category
	}{
		// Globally routable, which is the default for anything not enumerated.
		{"8.8.8.8", CategoryPublic},
		{"1.1.1.1", CategoryPublic},
		{"2001:4860:4860::8888", CategoryPublic},
		{"2606:4700:4700::1111", CategoryPublic},
		// The boundaries around each block below, from the outside.
		{"9.255.255.255", CategoryPublic},
		{"11.0.0.0", CategoryPublic},
		{"172.15.255.255", CategoryPublic},
		{"172.32.0.0", CategoryPublic},
		{"126.255.255.255", CategoryPublic},
		{"128.0.0.0", CategoryPublic},
		{"169.253.255.255", CategoryPublic},
		{"169.255.0.0", CategoryPublic},
		{"191.255.255.255", CategoryPublic},
		{"192.169.0.0", CategoryPublic},
		{"100.63.255.255", CategoryPublic},
		{"100.128.0.0", CategoryPublic},
		{"197.255.255.255", CategoryPublic},
		{"198.20.0.0", CategoryPublic},
		{"198.51.101.0", CategoryPublic},
		{"223.255.255.255", CategoryPublic},
		{"fe7f:ffff::1", CategoryPublic},
		// The far edge of fe80::/10, which is a /10 rather than a /16, and then
		// the retired site-local block that sits just past it.
		{"febf:ffff::1", CategoryLinkLocal},
		{"fec0::1", CategoryReserved},

		// The two address spaces a LAN is made of.
		{"10.0.0.0", CategoryPrivate},
		{"10.0.0.1", CategoryPrivate},
		{"10.255.255.255", CategoryPrivate},
		{"172.16.0.0", CategoryPrivate},
		{"172.31.255.255", CategoryPrivate},
		{"192.168.0.0", CategoryPrivate},
		{"192.168.1.10", CategoryPrivate},
		{"fc00::1", CategoryPrivate},
		{"fd12:3456:789a::1", CategoryPrivate},

		// This machine.
		{"127.0.0.1", CategoryLoopback},
		{"127.1.2.3", CategoryLoopback},
		{"127.255.255.255", CategoryLoopback},
		{"::1", CategoryLoopback},

		// One link.
		{"169.254.0.0", CategoryLinkLocal},
		{"169.254.1.1", CategoryLinkLocal},
		{"169.254.255.255", CategoryLinkLocal},
		{"fe80::1", CategoryLinkLocal},
		{"fe80::1%eth0", CategoryLinkLocal}, // a zone says which way out, not where to

		// The target that matters, named so that a log says what was aimed at.
		{"169.254.169.254", CategoryMetadata},

		// Carrier-grade NAT, whose range also holds a metadata service.
		{"100.64.0.0", CategoryShared},
		{"100.64.0.1", CategoryShared},
		{"100.100.100.200", CategoryShared},
		{"100.127.255.255", CategoryShared},

		// Allocated, and not a destination.
		{"0.0.0.0", CategoryReserved},
		{"0.255.255.255", CategoryReserved},
		{"192.0.0.1", CategoryReserved},
		{"192.0.2.5", CategoryReserved},
		{"192.88.99.1", CategoryReserved},
		{"198.18.0.0", CategoryReserved},
		{"198.19.255.255", CategoryReserved},
		{"198.51.100.9", CategoryReserved},
		{"203.0.113.9", CategoryReserved},
		{"224.0.0.0", CategoryReserved},
		{"239.255.255.255", CategoryReserved},
		{"240.0.0.0", CategoryReserved},
		{"255.255.255.255", CategoryReserved},
		{"::", CategoryReserved},
		{"::2", CategoryReserved}, // the retired IPv4-compatible spelling
		{"100::1", CategoryReserved},
		{"2001::1", CategoryReserved}, // Teredo, which carries an IPv4 address this refuses rather than unwraps
		{"2001:db8::1", CategoryReserved},
		{"2002::1", CategoryReserved}, // 6to4, on the same terms
		{"3fff::1", CategoryReserved},
		{"ff02::1", CategoryReserved},

		// Not an address at all. The zero Addr is what a parse failure leaves
		// behind, and it is refused rather than allowed by falling through.
		{"invalid", CategoryInvalid},
		{"", CategoryInvalid},
	}

	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			addr, err := netip.ParseAddr(test.address)
			if test.address == "invalid" || test.address == "" {
				if err == nil {
					t.Fatalf("ParseAddr(%q) parsed, so the case is not testing what it says", test.address)
				}
			} else if err != nil {
				t.Fatalf("ParseAddr(%q) error = %v", test.address, err)
			}
			if got := Classify(addr); got != test.category {
				t.Errorf("Classify(%s) = %s, want %s", test.address, got, test.category)
			}
		})
	}
}

// TestAnIPv4AddressWearingAnIPv6SpellingIsThatIPv4Address is §1.1's case, and
// the reason this package uses netip: net.IPNet.Contains does not know that
// ::ffff:127.0.0.1 is 127.0.0.1, reports it public, and hands an SSRF the
// loopback interface of the machine the product runs on.
func TestAnIPv4AddressWearingAnIPv6SpellingIsThatIPv4Address(t *testing.T) {
	tests := []struct {
		address  string
		category Category
	}{
		// Mapped (RFC 4291). The embedded address decides, whatever it is.
		{"::ffff:127.0.0.1", CategoryLoopback},
		{"::ffff:10.0.0.1", CategoryPrivate},
		{"::ffff:192.168.1.10", CategoryPrivate},
		{"::ffff:169.254.169.254", CategoryMetadata},
		{"::ffff:100.64.0.1", CategoryShared},
		{"::ffff:8.8.8.8", CategoryPublic},

		// Translated (RFC 6052, RFC 8215). On an IPv6-only network running
		// NAT64, 64:ff9b::7f00:1 is an ordinary way to write 127.0.0.1.
		{"64:ff9b::7f00:1", CategoryLoopback},
		{"64:ff9b::a00:1", CategoryPrivate},
		{"64:ff9b::a9fe:a9fe", CategoryMetadata},
		{"64:ff9b::808:808", CategoryPublic},
		{"64:ff9b:1::7f00:1", CategoryLoopback},

		// The same spelling with the zones and the case a URL may carry, because
		// a check that only recognises the way it was written in the test is a
		// check that recognises one spelling.
		{"::FFFF:127.0.0.1", CategoryLoopback},
		{"64:FF9B::7F00:1", CategoryLoopback},
	}

	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			addr := netip.MustParseAddr(test.address)
			if got := Classify(addr); got != test.category {
				t.Errorf("Classify(%s) = %s, want %s", test.address, got, test.category)
			}
		})
	}
}

// TestTheUnspecifiedAddressIsNotAnAddress pins the one value that is easy to
// pass in by accident: a netip.Addr from a failed parse is the zero value, and
// a classifier that treated it as public would turn every parse failure into a
// request.
func TestTheUnspecifiedAddressIsNotAnAddress(t *testing.T) {
	if got := Classify(netip.Addr{}); got != CategoryInvalid {
		t.Errorf("Classify(the zero Addr) = %s, want %s", got, CategoryInvalid)
	}
}
