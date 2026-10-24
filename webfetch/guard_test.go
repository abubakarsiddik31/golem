package webfetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestBlockedIP(t *testing.T) {
	cases := []struct {
		address string
		blocked bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.10", false}, // documentation range, still global unicast
		{"2606:4700:4700::1111", false},
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::1", true},
		{"::", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"ff02::1", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			if got := blockedIP(net.ParseIP(testCase.address)); got != testCase.blocked {
				t.Errorf("blockedIP(%s) = %v, want %v", testCase.address, got, testCase.blocked)
			}
		})
	}
	if !blockedIP(nil) {
		t.Error("blockedIP(nil) = false, want true")
	}
}

func TestBlockedAddressRejectedBeforeDialing(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",
		"http://10.1.2.3/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0/",
		"http://100.64.0.1/",
		"http://224.0.0.1/",
		"http://[::1]/",
		"http://[::]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			dialed := false
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				dialed = true
				return nil, errors.New("transport must not be reached")
			})}
			fetched := MustNew[struct{}](Config{HTTPClient: client})
			_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(target))
			if err == nil {
				t.Fatalf("Exec(%s) succeeded, want a blocked-address error", target)
			}
			if dialed {
				t.Error("the base transport ran for a blocked address")
			}
			var blocked *BlockedAddressError
			if !errors.As(err, &blocked) {
				t.Fatalf("Exec(%s) error = %v, want *BlockedAddressError in the chain", target, err)
			}
			if blocked.Host == "" || blocked.IP == nil {
				t.Errorf("BlockedAddressError = %+v, want host and IP populated", blocked)
			}
			var retry *model.ModelRetry
			if errors.As(err, &retry) {
				t.Error("a blocked address must not be a correctable model mistake")
			}
		})
	}
}

func TestHostnameResolvedToBlockedAddress(t *testing.T) {
	guard := newAddressGuard(false)
	guard.lookup = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.7")}, nil
	}
	dialed := false
	client := guardedClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		dialed = true
		return nil, errors.New("transport must not be reached")
	})}, guard)
	f := &fetcher{client: client, maxBytes: DefaultMaxBytes, userAgent: DefaultUserAgent}
	_, err := f.fetch(context.Background(), urlArgs("http://internal.example/"))
	var blocked *BlockedAddressError
	if !errors.As(err, &blocked) {
		t.Fatalf("fetch error = %v, want *BlockedAddressError in the chain", err)
	}
	if blocked.Host != "internal.example" || !blocked.IP.Equal(net.ParseIP("10.0.0.7")) {
		t.Errorf("BlockedAddressError = %+v, want internal.example / 10.0.0.7", blocked)
	}
	if dialed {
		t.Error("the base transport ran for a hostname that resolved to a blocked address")
	}
}

func TestHostnameResolutionFailureIsReported(t *testing.T) {
	guard := newAddressGuard(false)
	guard.lookup = func(context.Context, string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	}
	client := guardedClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport must not be reached")
	})}, guard)
	f := &fetcher{client: client, maxBytes: DefaultMaxBytes, userAgent: DefaultUserAgent}
	_, err := f.fetch(context.Background(), urlArgs("http://missing.example/"))
	if err == nil || !strings.Contains(err.Error(), "resolve missing.example") {
		t.Fatalf("fetch error = %v, want a resolve failure naming the host", err)
	}
	var blocked *BlockedAddressError
	if errors.As(err, &blocked) {
		t.Error("a resolution failure must not be reported as a blocked address")
	}
}

func TestLookupIPs(t *testing.T) {
	ips, err := lookupIPs(context.Background(), "localhost")
	if err != nil {
		t.Fatalf("lookupIPs(localhost) error = %v", err)
	}
	if len(ips) == 0 {
		t.Fatal("lookupIPs(localhost) returned no addresses")
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			t.Errorf("lookupIPs(localhost) returned non-loopback %s", ip)
		}
	}
}

func TestDialControlBlocksResolvedAddress(t *testing.T) {
	guard := newAddressGuard(false)
	if err := guard.control("tcp", "10.0.0.1:443", nil); err == nil {
		t.Error("control allowed a private dial address")
	}
	if err := guard.control("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("control rejected a public dial address: %v", err)
	}
	allowed := newAddressGuard(true)
	if err := allowed.control("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("AllowPrivateNetworks control rejected a loopback address: %v", err)
	}
}

func TestRedirectToBlockedAddressRejected(t *testing.T) {
	redirected := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "169.254.169.254" {
			redirected = true
			return nil, errors.New("transport must not be reached")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://169.254.169.254/latest/meta-data/"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}
	fetched := MustNew[struct{}](Config{HTTPClient: client})
	_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs("http://203.0.113.10/start"))
	var blocked *BlockedAddressError
	if !errors.As(err, &blocked) {
		t.Fatalf("Exec error = %v, want the redirect target blocked", err)
	}
	if blocked.Host != "169.254.169.254" {
		t.Errorf("BlockedAddressError.Host = %q, want the redirect target", blocked.Host)
	}
	if redirected {
		t.Error("the base transport ran for the blocked redirect target")
	}
}

func TestAllowPrivateNetworksPermitsLoopback(t *testing.T) {
	guard := newAddressGuard(true)
	target, err := url.Parse("http://127.0.0.1:8080/")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.checkURL(context.Background(), target); err != nil {
		t.Errorf("checkURL with AllowPrivateNetworks = %v, want nil", err)
	}
	if err := guard.checkURL(context.Background(), mustParseURL(t, "http://localhost/")); err != nil {
		t.Errorf("checkURL(localhost) with AllowPrivateNetworks = %v, want nil", err)
	}
}

func TestGuardedClientDoesNotMutateCaller(t *testing.T) {
	original := &http.Transport{}
	client := &http.Client{Transport: original}
	guarded := guardedClient(client, newAddressGuard(false))
	if client.Transport != original {
		t.Error("guardedClient replaced the caller's transport")
	}
	if guarded.Transport == original {
		t.Error("guardedClient should wrap the caller's transport")
	}
	if _, ok := guarded.Transport.(*guardedTransport); !ok {
		t.Errorf("guarded transport = %T, want *guardedTransport", guarded.Transport)
	}
}

func TestBlockedAddressErrorText(t *testing.T) {
	err := &BlockedAddressError{Host: "169.254.169.254", IP: net.ParseIP("169.254.169.254")}
	want := "webfetch: 169.254.169.254 resolves to blocked address 169.254.169.254"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
