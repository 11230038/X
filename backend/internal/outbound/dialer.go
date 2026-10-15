package outbound

// The boundary where it cannot be forgotten.
//
// Everything else in this package answers a question: may this address be
// reached? This file is what makes the answer matter, by putting it in the one
// place every connection has to pass through. §2.5 states why the hook is
// `DialContext` and not the call sites — a call site is a thing somebody has to
// remember, and "remember to call" is a comment rather than a mechanism, which
// the three clients already in this repository demonstrate: each of them was
// written by somebody who did not forget anything and none of them checks.
//
// What that buys and what it does not is worth being exact about, because the
// honest version is narrower than it sounds:
//
//   - It catches every connection this client makes, including the ones a retry
//     or a redirect causes and the ones written by whoever adds the fourth
//     client without reading this file.
//   - It does not catch a caller that sets a `Transport` of its own over the
//     top. That is a code-review question, not a runtime one, and no mechanism
//     in this package can answer it.
//   - It does not stop the name from being resolved: a name is still looked up,
//     so a resolver on another machine learns the name. What it stops is the
//     connection, and the thing being protected is where the request's contents
//     go.
//
// None of the three addresses these clients are given comes from request data —
// they are configuration, every one of them (§2.6). So what this closes is the
// door a mistyped base URL walks through, not a door a user can open. That
// sentence is the one to keep: this is not a vulnerability being fixed.

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// DialFunc is the shape http.Transport.DialContext takes, named so that a
// client can say it wants one without repeating a five-part signature in every
// constructor.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Dial timeouts. They are the defaults a bare net.Dialer would have used, kept
// here because the transport this is installed on no longer gets them from
// anywhere else.
const (
	dialTimeout   = 30 * time.Second
	dialKeepAlive = 30 * time.Second
)

// DialContext answers the dial function a policy describes, for an
// http.Transport to be built with.
//
// It reports an error rather than answering a function that refuses at the
// first call, because a deployment with no boundary is a wiring mistake and a
// wiring mistake belongs where the deployment starts — the same call the audit
// and the package service make about their own parts.
func DialContext(policy Policy) (DialFunc, error) {
	if policy == nil {
		return nil, ErrNotConfigured
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: dialKeepAlive}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			// The transport only ever hands over what a URL's host and port
			// resolved to, so this is a caller this package does not expect
			// rather than anything a user typed. It is still refused rather than
			// dialled unchecked, which is the rule in one sentence.
			return nil, fmt.Errorf("%w: %q is not a host and port", ErrBlocked, address)
		}
		addresses, err := resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		// Every address the name has, not the one that gets tried: see Check.
		if err := Check(policy, addresses); err != nil {
			return nil, err
		}
		// The addresses the policy saw are the addresses dialled. Handing the
		// *name* back to a dialer would be handing it a second resolution, and a
		// second resolution is a second answer — the name a policy allowed could
		// come back pointing at 127.0.0.1, and this whole boundary would have
		// been a check on a string.
		var lastErr error
		for _, addr := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}, nil
}

// resolve answers every address a host names: the address itself when it is
// already one, and otherwise what the resolver says.
//
// A literal is not a shortcut. It is the case the resolution would otherwise
// have to be trusted to get right, and it is the one a name-based check is
// usually defeated by.
func resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		// The resolver's own error is not carried: it names the host, and a host
		// is what a caller already knows. What is worth saying is that there was
		// nothing to check, which is a refusal and not a lookup that found
		// something allowed.
		return nil, fmt.Errorf("%w: %q does not resolve", ErrNoAddresses, host)
	}
	return addresses, nil
}
