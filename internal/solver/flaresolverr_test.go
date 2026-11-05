package solver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// fsServer mocks the FlareSolverr /v1 API: it records the request and answers
// with the handler's payload.
func fsServer(t *testing.T, handler func(req fsRequest) (int, string)) (*httptest.Server, *[]fsRequest) {
	t.Helper()
	var seen []fsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req fsRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body %q: %v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seen = append(seen, req)
		code, payload := handler(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func fsAnswer(status string, html string) string {
	resp := fsResponse{Status: status, Message: ""}
	resp.Solution.Response = html
	payload, _ := json.Marshal(resp)
	return string(payload)
}

func TestFlareSolverrSolvesThroughProxy(t *testing.T) {
	srv, seen := fsServer(t, func(req fsRequest) (int, string) {
		return http.StatusOK, fsAnswer("ok", string(readyHTML))
	})

	fs := NewFlareSolverr(srv.URL, 5*time.Second, slog.New(slog.DiscardHandler))
	entry := proxypool.Entry{
		Raw: "socks5h://user:secret@proxy.example:1080", Scheme: "socks5h",
		Host: "proxy.example", Port: "1080", Username: "user", Password: "secret",
	}
	body, err := fs.Fetch(t.Context(), "http://album.example/x", entry)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != string(readyHTML) {
		t.Errorf("body = %q, want the solved html", body)
	}

	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.Cmd != "request.get" || req.URL != "http://album.example/x" {
		t.Errorf("request = %+v, want request.get for the album url", req)
	}
	if req.Proxy == nil {
		t.Fatal("proxy missing: the solver must keep the crawler's route")
	}
	// socks5h maps to socks5; the credentials travel with the route
	if req.Proxy.Protocol != "socks5" || req.Proxy.Host != "proxy.example" || req.Proxy.Port != "1080" {
		t.Errorf("proxy = %+v, want socks5 proxy.example:1080", req.Proxy)
	}
	if req.Proxy.Username != "user" || req.Proxy.Password != "secret" {
		t.Errorf("credentials = %q/%q, want user/secret", req.Proxy.Username, req.Proxy.Password)
	}
}

func TestFlareSolverrDirectRouteOmitsProxy(t *testing.T) {
	srv, seen := fsServer(t, func(req fsRequest) (int, string) {
		return http.StatusOK, fsAnswer("ok", string(readyHTML))
	})

	fs := NewFlareSolverr(srv.URL, 5*time.Second, slog.New(slog.DiscardHandler))
	if _, err := fs.Fetch(t.Context(), "http://album.example/x", proxypool.Entry{}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if (*seen)[0].Proxy != nil {
		t.Errorf("proxy = %+v, want none on the direct route", (*seen)[0].Proxy)
	}
}

func TestFlareSolverrReportsServiceErrors(t *testing.T) {
	srv, _ := fsServer(t, func(req fsRequest) (int, string) {
		return http.StatusOK, `{"status":"error","message":"session has expired"}`
	})

	fs := NewFlareSolverr(srv.URL, 5*time.Second, slog.New(slog.DiscardHandler))
	_, err := fs.Fetch(t.Context(), "http://album.example/x", proxypool.Entry{})
	if err == nil {
		t.Fatal("Fetch() succeeded, want the service error")
	}
	if got := err.Error(); !strings.Contains(got, "session has expired") {
		t.Errorf("error = %q, want the service message inside", got)
	}
}

func TestFlareSolverrToleratesNonJSONGarbage(t *testing.T) {
	srv, _ := fsServer(t, func(req fsRequest) (int, string) {
		return http.StatusOK, "<html>not json</html>"
	})

	fs := NewFlareSolverr(srv.URL, 5*time.Second, slog.New(slog.DiscardHandler))
	if _, err := fs.Fetch(t.Context(), "http://album.example/x", proxypool.Entry{}); err == nil {
		t.Fatal("Fetch() succeeded, want a bad-response error")
	}
}
