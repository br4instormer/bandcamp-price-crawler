package proxypool

import (
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"

	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
)

// maxBodyBytes caps one response body. The fasthttp default of 4 MiB is
// tighter than the pre-fasthttp limit and would clip pathological pages.
const maxBodyBytes = 16 << 20

// maxRedirects mirrors the default net/http policy: fasthttp does not follow
// redirects on its own.
const maxRedirects = 10

// Proxy is one rotation slot: a proxy — or the direct connection — with its
// own pooled fasthttp client and health bookkeeping. Proxies come from
// Pool.Next; callers never build them.
type Proxy struct {
	entry   Entry // zero for the direct connection
	fast    *fasthttp.Client
	errs    atomic.Int64 // consecutive failed attempts, reset by a healthy one
	blocked atomic.Bool
}

// Label returns the masked proxy address for logs; the direct connection
// has none.
func (p *Proxy) Label() string {
	if p.entry.Raw == "" {
		return "direct"
	}
	return p.entry.String()
}

// Do performs the request through this proxy, following up to maxRedirects
// redirects the way the default net/http client did. It blocks until the
// exchange ends; the context and the request/response buffer lifecycle
// belong to the caller.
func (p *Proxy) Do(req *fasthttp.Request, resp *fasthttp.Response) error {
	return p.fast.DoRedirects(req, resp, maxRedirects)
}

// Pool rotates the configured proxies round-robin, one per HTTP attempt, and
// watches their health: a proxy that exhausts its error budget of
// consecutive failed attempts stays blocked until the pool is recreated (a
// run restart). When every proxy is blocked — or none are configured —
// requests take the direct connection (the null proxy), so Next always
// returns somewhere to send a request.
type Pool struct {
	null     *Proxy // direct connection; never blocked, never reported
	proxies  []*Proxy
	budget   int
	log      *slog.Logger
	onChange func(total, blocked int) // pushed to the dashboard on every block

	idx atomic.Int64 // round-robin cursor, advanced once per Next call
}

// New builds a pool serving the parsed entries. Entries may be empty — the
// pool then serves only the direct connection. onChange, when not nil, is
// called with (total, blocked) at build time and again on every block.
func New(entries []Entry, cfg *config.Config, log *slog.Logger, onChange func(total, blocked int)) *Pool {
	proxies := make([]*Proxy, 0, len(entries))
	for _, entry := range entries {
		proxies = append(proxies, newProxy(entry, cfg))
	}
	return newPool(proxies, cfg, log, onChange)
}

// newPool assembles the pool around ready proxies; tests inject their own
// dialers through it.
func newPool(proxies []*Proxy, cfg *config.Config, log *slog.Logger, onChange func(total, blocked int)) *Pool {
	p := &Pool{
		null:     newProxy(Entry{}, cfg),
		proxies:  proxies,
		budget:   cfg.ProxyErrorBudget,
		log:      log,
		onChange: onChange,
	}
	if p.onChange != nil {
		p.onChange(p.Stats())
	}
	return p
}

// newProxy builds one rotation slot. Every proxy gets its own client, so the
// pooled connections — and the CONNECT/SOCKS tunnels behind them — stay
// separate and one bad proxy cannot poison another's pool.
func newProxy(entry Entry, cfg *config.Config) *Proxy {
	// the direct connection dials with the request timeout; one dialer per
	// slot keeps its DNS cache private
	tcp := &fasthttp.TCPDialer{}
	client := &fasthttp.Client{
		Dial:                func(addr string) (net.Conn, error) { return tcp.DialTimeout(addr, cfg.HTTPTimeout) },
		MaxConnsPerHost:     cfg.MaxConcurrentRequests,
		MaxIdleConnDuration: 90 * time.Second, // the pre-fasthttp transport setting
		ReadTimeout:         cfg.HTTPTimeout,
		WriteTimeout:        cfg.HTTPTimeout,
		MaxResponseBodySize: maxBodyBytes,
		// the User-Agent travels with every request instead
		NoDefaultUserAgentHeader: true,
	}
	switch entry.Scheme {
	case "http", "https":
		// A CONNECT tunnel per pooled connection; the timeout also bounds
		// the tunnel setup. An https:// proxy opens TLS to the proxy itself.
		client.Dial = fasthttpproxy.FasthttpHTTPDialerTimeout(entry.Raw, cfg.HTTPTimeout)
	case "socks5", "socks5h":
		// socks5h resolves the target hostname at the proxy; credentials
		// come from the URL userinfo (SOCKS5 RFC 1929).
		client.Dial = fasthttpproxy.FasthttpSocksDialer(entry.Raw)
	}
	return &Proxy{entry: entry, fast: client}
}

// Next returns the proxy for one HTTP attempt: the next unblocked entry in
// round-robin order. With every proxy blocked, or none configured, the
// direct connection answers.
func (p *Pool) Next() *Proxy {
	for i := 0; i < len(p.proxies); i++ {
		proxy := p.proxies[int((p.idx.Add(1)-1)%int64(len(p.proxies)))]
		if !proxy.blocked.Load() {
			return proxy
		}
	}
	return p.null
}

// Report records one attempt's outcome. A healthy attempt — a success, or a
// status the proxy merely delivered, like an ordinary 404 — resets the
// consecutive-error counter; anything else counts toward the budget. A
// proxy that reaches the budget is blocked for the rest of the run.
func (p *Pool) Report(proxy *Proxy, healthy bool) {
	if proxy == nil || proxy == p.null {
		return // the direct connection is never blocked
	}
	if healthy {
		proxy.errs.Store(0)
		return
	}
	if proxy.errs.Add(1) >= int64(p.budget) && proxy.blocked.CompareAndSwap(false, true) {
		p.log.Warn("proxy blocked after too many consecutive errors",
			"proxy", proxy.Label(), "budget", p.budget)
		p.notify()
	}
}

// Stats returns the pool size and how many proxies are currently blocked.
func (p *Pool) Stats() (total, blocked int) {
	total = len(p.proxies)
	for _, proxy := range p.proxies {
		if proxy.blocked.Load() {
			blocked++
		}
	}
	return total, blocked
}

func (p *Pool) notify() {
	if p.onChange != nil {
		p.onChange(p.Stats())
	}
}
