package outbound

// What dialer.go claims, tested where it can be: a refusal happens before a
// connection is made, and an allowed address really is connected to.
//
// The second half is the one worth writing down. A suite of refusals passes
// against a dial function that refuses everything, and "everything is refused"
// is not a boundary — it is an outage that reports itself as a policy. So one
// case below opens a real socket on this machine and sends a byte through it.

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
)

func dialWith(t *testing.T, policy Policy) DialFunc {
	t.Helper()
	dial, err := DialContext(policy)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	return dial
}

// listenerOnThisMachine starts a server on loopback and answers the connection
// it accepts, so that a case can tell "a connection was made" from "an address
// was allowed". The other end is handed over on the channel; the channel is
// closed if the listener failed instead.
func listenerOnThisMachine(t *testing.T) (net.Listener, <-chan net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()
	return listener, accepted
}

// A policy that is handed an address nobody outside this deployment wrote is
// where this whole package is aimed: an address on the operator's own network,
// reached by a base URL that was mistyped or copied from somewhere else.
//
// Nothing is listening at either address, and that is deliberate: the refusal
// happens before a connection is attempted, so the answer does not depend on
// what is running on this machine or on whether the network is up.
func TestExternalMediaPolicyRefusesPrivateAddresses(t *testing.T) {
	cases := []struct {
		address string
		why     string
	}{
		{"10.0.0.5:8000", "a private address"},
		{"127.0.0.1:8000", "this machine"},
		{"169.254.169.254:80", "the metadata service"},
	}

	dial := dialWith(t, ExternalMediaPolicy{})
	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			conn, err := dial(context.Background(), "tcp", testCase.address)
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("dial error = %v, want ErrBlocked for %s", err, testCase.why)
			}
			if conn != nil {
				conn.Close()
				t.Fatal("a connection was returned alongside the refusal")
			}
			var blocked *Blocked
			if !errors.As(err, &blocked) {
				t.Fatalf("error = %v, want a *Blocked so that a log says which address it was", err)
			}
			host, _, _ := net.SplitHostPort(testCase.address)
			if blocked.Address.String() != host {
				t.Errorf("refused address = %q, want %q", blocked.Address, host)
			}
		})
	}
}

// The other half of the same claim: an allowed address is reached, and the
// connection carries what it is given.
func TestPolicyActuallyDialsAnAllowedAddress(t *testing.T) {
	listener, accepted := listenerOnThisMachine(t)

	conn, err := dialWith(t, LANServicePolicy{})(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	peer := <-accepted
	if peer == nil {
		t.Fatal("the listener was closed before it accepted anything")
	}
	defer peer.Close()

	// A socket that opened and cannot carry a byte is not a connection that was
	// made, and every interesting caller does this next.
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buffer := make([]byte, 1)
	if _, err := io.ReadFull(peer, buffer); err != nil {
		t.Fatalf("read: %v", err)
	}
	if buffer[0] != 'x' {
		t.Fatalf("read %q, want the byte that was written", buffer)
	}
}

// A name is not what is checked. The check is on what the name resolved to, and
// the two halves below are the same name with two answers: refused by the policy
// that does not reach this machine, reached by the one that does.
func TestANameIsJudgedByWhatItResolvesTo(t *testing.T) {
	listener, accepted := listenerOnThisMachine(t)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	// `localhost` is loopback — on a machine whose resolver answers at all — and
	// loopback is exactly what a mistyped base URL lands on.
	address := net.JoinHostPort("localhost", port)

	conn, err := dialWith(t, ExternalMediaPolicy{})(context.Background(), "tcp", address)
	if !errors.Is(err, ErrBlocked) {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("dial error = %v, want ErrBlocked for a name that resolves to this machine", err)
	}

	conn, err = dialWith(t, LANServicePolicy{})(context.Background(), "tcp", address)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	peer := <-accepted
	if peer == nil {
		t.Fatal("the listener was closed before it accepted anything")
	}
	defer peer.Close()
}

// .invalid is reserved by RFC 2606 for names that do not resolve, so this says
// what it means on any machine. It is not a refusal: nothing was there.
func TestANameThatDoesNotResolveIsRefused(t *testing.T) {
	conn, err := dialWith(t, LANServicePolicy{})(context.Background(), "tcp", "no-such-host.invalid:80")
	if !errors.Is(err, ErrNoAddresses) {
		t.Fatalf("dial error = %v, want ErrNoAddresses", err)
	}
	if conn != nil {
		conn.Close()
		t.Fatal("a connection was returned alongside the refusal")
	}
}

// The transport only ever hands over what a URL's host and port are, so this is
// a caller this package does not expect — and it is still refused rather than
// dialled on the chance that it was something else.
func TestSomethingThatIsNotAHostAndPortIsRefused(t *testing.T) {
	conn, err := dialWith(t, ExternalMediaPolicy{})(context.Background(), "tcp", "10.0.0.5")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("dial error = %v, want ErrBlocked", err)
	}
	if conn != nil {
		conn.Close()
		t.Fatal("a connection was returned alongside the refusal")
	}
}

// A deployment with no boundary is a wiring mistake, and it is answered where
// the deployment starts rather than by a dial function that lets everything
// through because nothing was configured to stop it.
func TestBuildingADialerWithoutAPolicyIsRefused(t *testing.T) {
	dial, err := DialContext(nil)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("DialContext() error = %v, want ErrNotConfigured", err)
	}
	if dial != nil {
		t.Fatal("a dial function was returned alongside the failure")
	}
}
