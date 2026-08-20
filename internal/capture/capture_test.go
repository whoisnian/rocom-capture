package capture

import (
	"net/netip"
	"testing"
)

func TestAllowSelfEndpoint(t *testing.T) {
	e := NewEngine(8195)
	ep := netip.MustParseAddrPort("192.0.2.10:45678")
	other := netip.MustParseAddrPort("192.0.2.10:45679")

	release1 := e.AllowSelfEndpoint(ep)
	release2 := e.AllowSelfEndpoint(ep)
	if !e.isAllowedSelfEndpoint(ep) {
		t.Fatal("registered endpoint is not allowed")
	}
	if e.isAllowedSelfEndpoint(other) {
		t.Fatal("unregistered endpoint is allowed")
	}

	release1()
	if !e.isAllowedSelfEndpoint(ep) {
		t.Fatal("endpoint was removed before all users released it")
	}
	release1() // release is idempotent
	release2()
	if e.isAllowedSelfEndpoint(ep) {
		t.Fatal("endpoint remains allowed after final release")
	}
}

func TestAllowedSelfEndpointOnlyOverridesItsOwnSkipIP(t *testing.T) {
	e := NewEngine(8195)
	local := netip.MustParseAddrPort("192.0.2.10:45678")
	remote := netip.MustParseAddrPort("198.51.100.20:8195")
	e.AddSkipIP(local.Addr())

	if !e.shouldSkipEndpoints(local, remote) {
		t.Fatal("unregistered local endpoint was not skipped")
	}
	release := e.AllowSelfEndpoint(local)
	defer release()
	if e.shouldSkipEndpoints(local, remote) || e.shouldSkipEndpoints(remote, local) {
		t.Fatal("registered SOCKS5 endpoint was skipped")
	}

	e.AddSkipIP(remote.Addr())
	if !e.shouldSkipEndpoints(local, remote) {
		t.Fatal("allowed local endpoint overrode the remote ignored IP")
	}
}
