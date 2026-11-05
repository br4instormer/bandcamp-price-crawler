package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// maxFSBodyBytes caps one FlareSolverr response body; the HTML inside can be
// as large as any album page.
const maxFSBodyBytes = 16 << 20

// FlareSolverr solves the challenge through a FlareSolverr-compatible
// service — its /v1 API's request.get command returns the page HTML after
// the challenge script has run.
type FlareSolverr struct {
	base       string // no trailing slash
	maxTimeout time.Duration
	client     *http.Client
}

// NewFlareSolverr points the solver at the service base URL, e.g.
// http://trawl.local. maxTimeout is the per-request solving budget the
// service itself honors.
func NewFlareSolverr(base string, maxTimeout time.Duration, _ *slog.Logger) *FlareSolverr {
	return &FlareSolverr{
		base:       strings.TrimRight(base, "/"),
		maxTimeout: maxTimeout,
		client:     &http.Client{Timeout: maxTimeout + 15*time.Second},
	}
}

// Name identifies the solver in logs and sticky modes.
func (f *FlareSolverr) Name() string { return "flaresolverr" }

type fsRequest struct {
	Cmd        string   `json:"cmd"`
	URL        string   `json:"url"`
	MaxTimeout int      `json:"maxTimeout"`
	Proxy      *fsProxy `json:"proxy,omitempty"`
}

// fsProxy carries the crawler's proxy route into the service; the page is
// fetched through it, so the challenge sees the same exit IP as fasthttp.
type fsProxy struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type fsResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL      string `json:"url"`
		Status   int    `json:"status"`
		Response string `json:"response"`
	} `json:"solution"`
}

// Fetch solves the challenge for url over the given proxy route. The
// returned HTML is checked for the album markup by Chain.Solve.
func (f *FlareSolverr) Fetch(ctx context.Context, url string, entry proxypool.Entry) ([]byte, error) {
	req := fsRequest{
		Cmd:        "request.get",
		URL:        url,
		MaxTimeout: int(f.maxTimeout.Milliseconds()),
	}
	if entry.Raw != "" {
		protocol := entry.Scheme
		if protocol == "socks5h" {
			protocol = "socks5"
		}
		req.Proxy = &fsProxy{
			Protocol: protocol,
			Host:     entry.Host,
			Port:     entry.Port,
			Username: entry.Username,
			Password: entry.Password,
		}
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+"/v1", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFSBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("flaresolverr: reading response: %w", err)
	}

	var parsed fsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("flaresolverr: bad response: %w", err)
	}
	if parsed.Status != "ok" {
		msg := parsed.Message
		if msg == "" {
			msg = parsed.Status
		}
		return nil, fmt.Errorf("flaresolverr: %s", msg)
	}
	return []byte(parsed.Solution.Response), nil
}
