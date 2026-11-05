package proxypool

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
)

func testConfig(budget int) *config.Config {
	return &config.Config{
		MaxConcurrentRequests: 4,
		HTTPTimeout:           2 * time.Second,
		ProxyErrorBudget:      budget,
	}
}

// dummyProxy builds a rotation slot with the given label; Next() never dials,
// so a dead dialer is fine for rotation tests.
func dummyProxy(label string) *Proxy {
	return &Proxy{
		entry: Entry{Raw: label, Scheme: "http", Host: label, Port: "1"},
		fast:  &fasthttp.Client{Dial: func(string) (net.Conn, error) { return nil, net.ErrClosed }},
	}
}

func TestNextRotatesRoundRobin(t *testing.T) {
	p := newPool([]*Proxy{dummyProxy("a"), dummyProxy("b"), dummyProxy("c")},
		testConfig(5), slog.New(slog.DiscardHandler), nil)

	counts := map[string]int{}
	for range 9 {
		counts[p.Next().Label()]++
	}
	for _, label := range []string{"a", "b", "c"} {
		if counts[label] != 3 {
			t.Errorf("proxy %q served %d calls, want 3", label, counts[label])
		}
	}
}

func TestNextSkipsBlocked(t *testing.T) {
	p := newPool([]*Proxy{dummyProxy("a"), dummyProxy("b"), dummyProxy("c")},
		testConfig(5), slog.New(slog.DiscardHandler), nil)
	p.proxies[0].blocked.Store(true) // block by hand: budget is irrelevant here

	for range 6 {
		if got := p.Next().Label(); got == "a" {
			t.Fatal("blocked proxy a must not rotate")
		}
	}
}

func TestNextAllBlockedFallsBackToDirect(t *testing.T) {
	p := newPool([]*Proxy{dummyProxy("a"), dummyProxy("b")},
		testConfig(5), slog.New(slog.DiscardHandler), nil)
	for _, proxy := range p.proxies {
		proxy.blocked.Store(true)
	}

	if got := p.Next().Label(); got != "direct" {
		t.Errorf("Next() with every proxy blocked = %q, want %q", got, "direct")
	}
}

func TestNextWithoutProxiesIsDirect(t *testing.T) {
	p := newPool(nil, testConfig(5), slog.New(slog.DiscardHandler), nil)
	if got := p.Next().Label(); got != "direct" {
		t.Errorf("Next() without proxies = %q, want %q", got, "direct")
	}
}

func TestReportBlocksAfterConsecutiveErrors(t *testing.T) {
	p := newPool([]*Proxy{dummyProxy("a")}, testConfig(3), slog.New(slog.DiscardHandler), nil)
	proxy := p.proxies[0]

	// two errors are under the budget
	p.Report(proxy, false)
	p.Report(proxy, false)
	if _, blocked := p.Stats(); blocked != 0 {
		t.Fatal("proxy blocked under the budget")
	}

	// a healthy attempt resets the consecutive counter
	p.Report(proxy, true)
	p.Report(proxy, false)
	p.Report(proxy, false)
	if _, blocked := p.Stats(); blocked != 0 {
		t.Fatal("success must reset the error counter")
	}

	// the third error in a row exhausts the budget
	p.Report(proxy, false)
	if _, blocked := p.Stats(); blocked != 1 {
		t.Error("proxy must block after three consecutive errors")
	}

	// errors after the block are not double-counted anywhere
	p.Report(proxy, false)
	if _, blocked := p.Stats(); blocked != 1 {
		t.Error("blocked proxy must stay counted once")
	}
}

func TestReportNeverBlocksDirect(t *testing.T) {
	p := newPool(nil, testConfig(1), slog.New(slog.DiscardHandler), nil)
	for range 10 {
		p.Report(p.Next(), false) // Next() is the direct connection
	}
	if _, blocked := p.Stats(); blocked != 0 {
		t.Error("the direct connection must never block")
	}
}

func TestHealthy404DoesNotCountAsError(t *testing.T) {
	// the classification lives in httpclient; here only the counter contract
	// is checked: healthy resets, unhealthy counts
	p := newPool([]*Proxy{dummyProxy("a")}, testConfig(2), slog.New(slog.DiscardHandler), nil)
	proxy := p.proxies[0]
	p.Report(proxy, true)
	p.Report(proxy, true)
	p.Report(proxy, false)
	if _, blocked := p.Stats(); blocked != 0 {
		t.Error("healthy attempts must keep the proxy in rotation")
	}
}

func TestOnChangeNotified(t *testing.T) {
	var states [][2]int
	onChange := func(total, blocked int) { states = append(states, [2]int{total, blocked}) }

	p := newPool([]*Proxy{dummyProxy("a"), dummyProxy("b")}, testConfig(1),
		slog.New(slog.DiscardHandler), onChange)
	p.Report(p.proxies[0], false) // budget 1: blocks at once

	if len(states) != 2 {
		t.Fatalf("onChange fired %d times, want 2 (build + block)", len(states))
	}
	if states[0] != [2]int{2, 0} || states[1] != [2]int{2, 1} {
		t.Errorf("onChange states = %v, want [2 0] then [2 1]", states)
	}
}

// TestDoThroughHTTPProxy drives the real fasthttpproxy dialer through a
// CONNECT tunnel: the target is reached with the proxy in the middle.
func TestDoThroughHTTPProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through the tunnel")
	}))
	defer target.Close()

	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		dst, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer dst.Close()
		src, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer src.Close()
		if _, err := src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		go func() { _, _ = io.Copy(dst, buf); dst.Close() }() // client → target
		_, _ = io.Copy(src, dst)                              // target → client
	}))
	defer proxySrv.Close()

	proxyHost, proxyPort, _ := net.SplitHostPort(proxySrv.Listener.Addr().String())
	p := newPool([]*Proxy{newProxy(Entry{
		Raw:    "http://" + net.JoinHostPort(proxyHost, proxyPort),
		Scheme: "http", Host: proxyHost, Port: proxyPort,
	}, testConfig(3))}, testConfig(3), slog.New(slog.DiscardHandler), nil)

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(target.URL)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	if err := p.Next().Do(req, resp); err != nil {
		t.Fatalf("Do() through the proxy = %v", err)
	}
	if resp.StatusCode() != http.StatusOK || string(resp.Body()) != "through the tunnel" {
		t.Errorf("status/body = %d/%q, want 200/through the tunnel", resp.StatusCode(), resp.Body())
	}
}
