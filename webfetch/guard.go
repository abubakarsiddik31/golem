package webfetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// BlockedAddressError reports a fetch refused because the destination is
// not a public internet address. Loopback, private, link-local,
// multicast, unspecified, and carrier-grade NAT addresses are blocked by
// default so a model-produced URL cannot reach internal services or cloud
// metadata endpoints. Set Config.AllowPrivateNetworks to opt out.
//
// A blocked address is a policy failure, not a correctable model mistake:
// the run fails at the tool stage with this error in the chain.
type BlockedAddressError struct {
	// Host is the hostname or IP literal from the request URL.
	Host string
	// IP is the address that was refused.
	IP net.IP
}

func (e *BlockedAddressError) Error() string {
	return fmt.Sprintf("webfetch: %s resolves to blocked address %s", e.Host, e.IP)
}

// addressGuard enforces the tool's network policy for one fetcher. The
// lookup seam lets tests resolve hostnames without touching DNS.
type addressGuard struct {
	allowPrivate bool
	lookup       func(context.Context, string) ([]net.IP, error)
}

func newAddressGuard(allowPrivate bool) *addressGuard {
	return &addressGuard{allowPrivate: allowPrivate, lookup: lookupIPs}
}

// lookupIPs resolves host through the process resolver.
func lookupIPs(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, address.IP)
	}
	return ips, nil
}

// blockedIP reports whether ip is outside the public internet. The check
// is deliberately conservative: an address only passes when it is global
// unicast, not private, and not carrier-grade NAT.
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if !ip.IsGlobalUnicast() {
		// Loopback, link-local, multicast, and the unspecified address.
		return true
	}
	if ip.IsPrivate() {
		// RFC 1918 and RFC 4193 unique-local.
		return true
	}
	// 100.64.0.0/10 (RFC 6598) is not "private" to net.IP but is
	// routinely used for internal networks.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		return true
	}
	return false
}

// checkURL validates the destination of one request. Every request the
// client issues passes through it, so redirects are re-validated on each
// hop. Hostnames are resolved and every returned address must pass; this
// is conservative and closes the gap for custom transports that do not
// use the guarded dialer.
func (g *addressGuard) checkURL(ctx context.Context, target *url.URL) error {
	if g.allowPrivate || target == nil {
		return nil
	}
	host := target.Hostname()
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return g.checkIP(host, ip)
	}
	ips, err := g.lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("webfetch: resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("webfetch: resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if err := g.checkIP(host, ip); err != nil {
			return err
		}
	}
	return nil
}

func (g *addressGuard) checkIP(host string, ip net.IP) error {
	if g.allowPrivate {
		return nil
	}
	if blockedIP(ip) {
		return &BlockedAddressError{Host: host, IP: ip}
	}
	return nil
}

// control is the dialer's last line of defence: it sees the address the
// resolver actually produced, so a hostname that re-resolves between the
// pre-check and the dial still cannot reach an internal address.
func (g *addressGuard) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webfetch: dial %s: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webfetch: dial %s: not an IP address", address)
	}
	return g.checkIP(host, ip)
}

// dialContext dials with the guard attached to the dialer.
func (g *addressGuard) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   g.control,
	}
	return dialer.DialContext(ctx, network, address)
}

// guardedTransport validates every request before it reaches the base
// transport, which also covers each redirect hop.
type guardedTransport struct {
	base  http.RoundTripper
	guard *addressGuard
}

func (t *guardedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.guard.checkURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

// guardedClient returns a client that enforces guard without mutating the
// caller's client. A nil client selects the tool's default client with
// DefaultTimeout and a guarded dialer. A caller-supplied *http.Transport
// is cloned; its own DialContext is preserved, in which case the
// per-request resolution check remains the enforcement point.
func guardedClient(client *http.Client, guard *addressGuard) *http.Client {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = guard.dialContext
		return &http.Client{
			Timeout:   DefaultTimeout,
			Transport: &guardedTransport{base: transport, guard: guard},
		}
	}

	guarded := *client
	base := guarded.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		clone := transport.Clone()
		if clone.DialContext == nil || base == http.DefaultTransport {
			clone.DialContext = guard.dialContext
		}
		base = clone
	}
	guarded.Transport = &guardedTransport{base: base, guard: guard}
	return &guarded
}
