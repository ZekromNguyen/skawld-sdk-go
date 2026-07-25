package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// NetworkPolicy constrains model-selected HTTP destinations. The zero value
// permits public HTTP(S) hosts and rejects loopback, private, link-local,
// multicast, and unspecified addresses.
type NetworkPolicy struct {
	AllowedHosts []string
	AllowPrivate bool
}

func (p NetworkPolicy) ValidateURL(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, core.NewPermissionError("network URL must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return nil, core.NewPermissionError("network URL userinfo is not allowed")
	}
	if !p.hostAllowed(parsed.Hostname()) {
		return nil, core.NewPermissionError(fmt.Sprintf("network host %q is not allowed", parsed.Hostname()))
	}
	if p.AllowPrivate {
		return parsed, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve network host %q: %w", parsed.Hostname(), err)
	}
	if len(addresses) == 0 {
		return nil, core.NewPermissionError("network host resolved to no addresses")
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return nil, core.NewPermissionError(fmt.Sprintf("network host %q resolves to a non-public address", parsed.Hostname()))
		}
	}
	return parsed, nil
}

func (p NetworkPolicy) hostAllowed(host string) bool {
	if len(p.AllowedHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range p.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), "."))
		if host == allowed || (strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, allowed[1:])) {
			return true
		}
	}
	return false
}

func publicIP(ip net.IP) bool {
	return ip != nil &&
		ip.IsGlobalUnicast() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified() &&
		!inCGNAT(ip)
}

func inCGNAT(ip net.IP) bool {
	_, network, _ := net.ParseCIDR("100.64.0.0/10")
	return network.Contains(ip)
}

func safeHTTPClient(base *http.Client, policy NetworkPolicy) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if base != nil {
		*client = *base
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if configured, ok := client.Transport.(*http.Transport); ok {
		transport = configured.Clone()
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if !policy.hostAllowed(host) {
			return nil, core.NewPermissionError(fmt.Sprintf("network host %q is not allowed", host))
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if !policy.AllowPrivate && !publicIP(candidate.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, core.NewPermissionError(fmt.Sprintf("network host %q has no permitted address", host))
	}
	// A configured DialTLSContext would bypass the policy-aware DialContext.
	transport.DialTLSContext = nil
	client.Transport = transport
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if _, err := policy.ValidateURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return client
}
