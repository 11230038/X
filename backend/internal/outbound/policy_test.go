package outbound

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// TestTheSameAddressIsRefusedByOnePolicyAndAllowedByTheOther is the whole of
// §2.12 and the reason this package has two types instead of one function with
// a flag.
//
// A media URL inside a message is written by whoever sent the message, so a
// private address there is somebody aiming the product at the deployment's own
// network. A service the operator configured — the document parser — is normally
// on that same network, and the default this build ships with is loopback. One
// address, two answers, and the difference is the origin and not the address.
func TestTheSameAddressIsRefusedByOnePolicyAndAllowedByTheOther(t *testing.T) {
	// Loopback is in this list rather than in the refusals below because the
	// parser's own default is `http://127.0.0.1:5001`: a LAN policy that refused
	// it would refuse the configuration the deployment starts with.
	lan := []string{
		"192.168.1.10", "10.0.0.5", "172.20.0.1", "fd12:3456::1",
		"127.0.0.1", "::1", "::ffff:127.0.0.1",
	}
	external := Policy(ExternalMediaPolicy{})
	service := Policy(LANServicePolicy{})

	for _, text := range lan {
		addr := netip.MustParseAddr(text)
		if external.Allow(addr) {
			t.Errorf("ExternalMediaPolicy allowed %s, which was written by whoever sent the message", text)
		}
		if !service.Allow(addr) {
			t.Errorf("LANServicePolicy refused %s, which the operator wrote down about their own network", text)
		}
	}

	// And they agree about the public internet, which is the other half: the
	// two policies differ in one place, not generally.
	public := netip.MustParseAddr("93.184.216.34")
	if !external.Allow(public) || !service.Allow(public) {
		t.Error("a public address was refused by a policy, want both to allow it")
	}
}

// TestWhatBothPoliciesRefuse states the intersection, so that the difference
// asserted above cannot be widened: "the operator's own network" is not
// "whatever is nearby", and the widening stops at private space and this host.
//
// Loopback is deliberately absent — the LAN policy allows it for the reason the
// case above gives — and what is left is the set neither policy will ever
// reach, which is the set that would be a defect in both.
func TestWhatBothPoliciesRefuse(t *testing.T) {
	tests := []struct {
		address string
		why     string
	}{
		{"169.254.169.254", "the metadata service hands out credentials"},
		{"169.254.1.1", "link-local is not a destination"},
		{"fe80::1%eth0", "link-local is not a destination"},
		{"100.100.100.200", "a metadata service inside carrier-grade NAT"},
		{"0.0.0.0", "not an address"},
		{"224.0.0.1", "multicast"},
		{"255.255.255.255", "the broadcast address"},
		{"invalid", "not an address at all"},
	}

	policies := map[string]Policy{
		"ExternalMediaPolicy": ExternalMediaPolicy{},
		"LANServicePolicy":    LANServicePolicy{},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			addr, err := netip.ParseAddr(test.address)
			if err != nil && test.address != "invalid" {
				t.Fatalf("ParseAddr(%q) error = %v", test.address, err)
			}
			for name, policy := range policies {
				if policy.Allow(addr) {
					t.Errorf("%s allowed %s, want it refused because %s", name, test.address, test.why)
				}
			}
		})
	}
}

// TestThePoliciesAreTwoTypes is the structural half of the rule: §13.1 第 7 条
// forbids merging the two into one permissive implementation, and the merge
// happens by turning the difference into a parameter. Two types cannot be
// passed wrongly by a caller who never looked at them.
func TestThePoliciesAreTwoTypes(t *testing.T) {
	external := reflect.TypeOf(ExternalMediaPolicy{})
	lan := reflect.TypeOf(LANServicePolicy{})
	if external == lan {
		t.Fatalf("the two policies are one type, %s", external)
	}
	// Where the difference is not a value, it cannot be defaulted, zeroed or
	// forgotten in whichever direction is dangerous.
	for _, policy := range []reflect.Type{external, lan} {
		if policy.Kind() != reflect.Struct || policy.NumField() != 0 {
			t.Errorf("%s carries state, so its answer depends on how it was built", policy)
		}
	}
}

// TestANameWithSeveralAddressesIsAllowedOnlyIfAllOfThemAre: a name the attacker
// controls resolves to as many addresses as they like, so a check that stopped
// at the first allowed one would pass a request on to a client that then picked
// the other.
func TestANameWithSeveralAddressesIsAllowedOnlyIfAllOfThemAre(t *testing.T) {
	good := netip.MustParseAddr("93.184.216.34")
	alsoGood := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
	loopback := netip.MustParseAddr("127.0.0.1")

	if err := Check(ExternalMediaPolicy{}, []netip.Addr{good, alsoGood}); err != nil {
		t.Fatalf("Check() error = %v, want a name with only public addresses to be allowed", err)
	}

	err := Check(ExternalMediaPolicy{}, []netip.Addr{good, loopback})
	if err == nil {
		t.Fatal("Check() allowed a name that also resolved to loopback")
	}
	// The refusal names the address, not the name: the name is what the sender
	// controls and wrote, and what an operator has to look at is where it pointed.
	var blocked *Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Check() error = %v (%T), want a *Blocked", err, err)
	}
	if blocked.Address != loopback || blocked.Category != CategoryLoopback {
		t.Errorf("Blocked = %+v, want the loopback address and its category", blocked)
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("errors.Is(%v, ErrBlocked) = false, want a caller to recognise the boundary", err)
	}
	// The address is in the sentence, because the sentence is what an operator
	// reads in a log after a request was refused.
	if !strings.Contains(err.Error(), "127.0.0.1") || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("Error() = %q, want it to name the address and what it is", err)
	}

	// The same addresses through the other policy: the LAN one allows the
	// private address it was written for and the loopback the parser defaults
	// to, and still refuses what came with them.
	private := netip.MustParseAddr("192.168.1.10")
	if err := Check(LANServicePolicy{}, []netip.Addr{private, good}); err != nil {
		t.Errorf("Check() error = %v, want a LAN service on a private address to be allowed", err)
	}
	if err := Check(LANServicePolicy{}, []netip.Addr{private, loopback}); err != nil {
		t.Errorf("Check() error = %v, want loopback allowed for a service beside the product", err)
	}
	// One address each of the two kinds the widening does not reach, so that
	// "allows loopback" cannot quietly become "allows anything on this host's
	// link", which is where the credentials are.
	for _, refused := range []netip.Addr{
		netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("fe80::1"),
	} {
		if err := Check(LANServicePolicy{}, []netip.Addr{good, refused}); !errors.Is(err, ErrBlocked) {
			t.Errorf("Check(%s) error = %v, want it refused even beside a public address", refused, err)
		}
	}
}

// TestCheckRefusesWhatItCannotCheck covers the two ways the boundary could fail
// open: nothing resolved, and no policy was wired up. Neither is a request that
// may be made.
func TestCheckRefusesWhatItCannotCheck(t *testing.T) {
	if err := Check(ExternalMediaPolicy{}, nil); !errors.Is(err, ErrNoAddresses) {
		t.Errorf("Check(policy, nil) error = %v, want ErrNoAddresses", err)
	}
	if err := Check(ExternalMediaPolicy{}, []netip.Addr{}); !errors.Is(err, ErrNoAddresses) {
		t.Errorf("Check(policy, empty) error = %v, want ErrNoAddresses", err)
	}
	if err := Check(nil, []netip.Addr{netip.MustParseAddr("8.8.8.8")}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Check(nil, addresses) error = %v, want ErrNotConfigured", err)
	}
}
