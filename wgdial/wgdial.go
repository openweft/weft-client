// Package wgdial adapts the userspace WireGuard transport
// (github.com/grpc-transports/wireguard) into a weft-client dial option,
// letting the CLI reach a micro-VM's gRPC endpoint over an end-to-end
// WireGuard overlay — encrypted even against an untrusted hypervisor host,
// and requiring no root or interface configuration on the operator's machine.
//
// This lives in its own package so the gVisor / wireguard-go dependency tree
// is pulled in only by binaries that actually dial over WireGuard. Callers
// that only use the local socket or SSH transport never compile it.
//
// Usage:
//
//	opt, err := wgdial.Option("10.0.0.5:50051", wgtransport.ClientConfig{
//	    PrivateKeyPath: "~/.weft/wg_priv",
//	    LocalIP:        netip.MustParseAddr("10.0.0.99"),
//	    Peer: wgtransport.Peer{
//	        PublicKey:  vmPubKey,
//	        AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
//	        Endpoint:   "vm-host.dc1.example:51820",
//	        PersistentKeepalive: 25,
//	    },
//	})
//	if err != nil { ... }
//	conn, err := weftclient.Dial("", opt)
package wgdial

import (
	wgtransport "github.com/grpc-transports/wireguard"
	weftclient "github.com/openweft/weft-client"
)

// Option builds a weft-client Option that tunnels the gRPC connection to the
// micro-VM at overlay address `target` ("ip:port") through the userspace
// WireGuard device described by cfg. The WireGuard device is created when this
// is called; reuse the returned Option across Dial calls rather than rebuilding
// it per connection.
func Option(target string, cfg wgtransport.ClientConfig) (weftclient.Option, error) {
	opt, err := wgtransport.DialOption(target, cfg)
	if err != nil {
		return nil, err
	}
	return weftclient.WithDialOption("passthrough:///"+target, opt), nil
}
