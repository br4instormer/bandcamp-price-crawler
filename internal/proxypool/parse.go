// Package proxypool parses the HTTP_PROXIES specification and rotates the
// proxies round-robin, blocking the ones that exhaust their error budget.
// A pool with no proxies — or with every proxy blocked — serves the direct
// connection (the null proxy), so a request always has somewhere to go;
// proxy-only mode (config.Config.ProxyOnly) drops the null proxy instead and
// lets Next report the outage with nil.
package proxypool

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// defaultPorts fill in proxies written without an explicit port.
var defaultPorts = map[string]string{
	"http":    "80",
	"https":   "443",
	"socks5":  "1080",
	"socks5h": "1080",
}

// Entry is one parsed proxy of the HTTP_PROXIES specification.
type Entry struct {
	// Raw is the original spec line, credentials included: the dialers
	// receive it verbatim.
	Raw string
	// Scheme is http, https, socks5 or socks5h (lowercase).
	Scheme string
	// Host and Port form the proxy address; Port falls back to the scheme
	// default when the spec omits it.
	Host, Port string
	// Username and Password come from the URL userinfo: basic auth for
	// http(s) proxies, SOCKS5 RFC 1929 for socks5(h).
	Username, Password string
}

// String returns the masked proxy address: passwords never reach logs.
func (e Entry) String() string {
	if e.Username == "" && e.Password == "" {
		return e.Raw
	}
	return fmt.Sprintf("%s://%s:***@%s:%s", e.Scheme, e.Username, e.Host, e.Port)
}

// Parse decodes the HTTP_PROXIES value. A value containing "://" is an inline
// comma-separated proxy list; anything else names a file holding one proxy
// per line. An empty or missing value means no proxies. Invalid proxies are
// dropped with a debug log entry — one broken entry never discards the rest.
func Parse(spec string, log *slog.Logger) []Entry {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}

	lines := strings.Split(spec, ",")
	if !strings.Contains(spec, "://") {
		lines = readLines(spec, log)
	}

	var entries []Entry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, err := parseEntry(line)
		if err != nil {
			log.Debug("dropping invalid proxy", "proxy", line, "error", err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// readLines loads a proxy list file, dropping blank lines and #-comments. An
// unreadable file quietly yields no proxies: the crawl runs on the direct
// connection instead of aborting.
func readLines(path string, log *slog.Logger) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Debug("cannot read the proxy list file, running without proxies", "path", path, "error", err)
		return nil
	}

	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseEntry(line string) (Entry, error) {
	u, err := url.Parse(line)
	if err != nil {
		return Entry{}, err
	}
	port, ok := defaultPorts[strings.ToLower(u.Scheme)]
	if !ok {
		return Entry{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return Entry{}, errors.New("missing host")
	}

	entry := Entry{
		Raw:    line,
		Scheme: strings.ToLower(u.Scheme),
		Host:   u.Hostname(),
		Port:   u.Port(),
	}
	if entry.Port == "" {
		entry.Port = port
	}
	if u.User != nil {
		entry.Username = u.User.Username()
		entry.Password, _ = u.User.Password()
	}
	return entry, nil
}
