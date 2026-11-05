package proxypool

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestParseEmptyMeansNoProxies(t *testing.T) {
	for _, spec := range []string{"", "   ", "\n"} {
		if got := Parse(spec, testLogger()); got != nil {
			t.Errorf("Parse(%q) = %v, want nil", spec, got)
		}
	}
}

func TestParseInlineList(t *testing.T) {
	spec := "https://proxy.server:8080, socks5://socks5.proxies:1080, socks5://user:password@authenticated.proxy:1080"

	entries := Parse(spec, testLogger())
	if len(entries) != 3 {
		t.Fatalf("Parse() = %d entries, want 3", len(entries))
	}

	if entries[0].Scheme != "https" || entries[0].Host != "proxy.server" || entries[0].Port != "8080" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].Scheme != "socks5" || entries[1].Host != "socks5.proxies" || entries[1].Port != "1080" {
		t.Errorf("entry 1 = %+v", entries[1])
	}
	if entries[2].Username != "user" || entries[2].Password != "password" ||
		entries[2].Host != "authenticated.proxy" || entries[2].Port != "1080" {
		t.Errorf("entry 2 = %+v", entries[2])
	}
}

func TestParseSingleProxyIsValid(t *testing.T) {
	entries := Parse("socks5://127.0.0.1:9050", testLogger())
	if len(entries) != 1 || entries[0].Host != "127.0.0.1" || entries[0].Port != "9050" {
		t.Errorf("Parse() = %+v, want one localhost:9050 entry", entries)
	}
}

func TestParseFileList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.txt")
	// the spec example plus comments, blank lines and stray whitespace
	content := "# internal pool\n" +
		"https://proxy.server:8080\n" +
		"\n" +
		"   \n" +
		"socks5://socks5.proxies:1080\n" +
		"  socks5://user:password@authenticated.proxy:1080  \n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := Parse(path, testLogger())
	if len(entries) != 3 {
		t.Fatalf("Parse(%q) = %d entries, want 3", path, len(entries))
	}
	if entries[0].Scheme != "https" || entries[2].Password != "password" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestParseMissingFileIsQuietlyEmpty(t *testing.T) {
	if got := Parse(filepath.Join(t.TempDir(), "absent.txt"), testLogger()); got != nil {
		t.Errorf("Parse() on a missing file = %v, want nil", got)
	}
}

func TestParseDropsInvalidEntries(t *testing.T) {
	// one broken entry must not discard the rest
	spec := "socks4://legacy:1080, ://no-scheme-host, just-text, http://, https://valid.proxy:8080"

	entries := Parse(spec, testLogger())
	if len(entries) != 1 {
		t.Fatalf("Parse() = %d entries, want 1", len(entries))
	}
	if entries[0].Host != "valid.proxy" {
		t.Errorf("surviving entry = %+v, want valid.proxy", entries[0])
	}
}

func TestParseDefaultPorts(t *testing.T) {
	tests := []struct {
		spec, want string
	}{
		{"http://p.proxy", "80"},
		{"https://p.proxy", "443"},
		{"socks5://p.proxy", "1080"},
		{"socks5h://p.proxy", "1080"},
	}
	for _, tt := range tests {
		entries := Parse(tt.spec, testLogger())
		if len(entries) != 1 || entries[0].Port != tt.want {
			t.Errorf("Parse(%q) port = %v, want %q", tt.spec, entries, tt.want)
		}
	}
}

func TestParseUppercaseScheme(t *testing.T) {
	entries := Parse("SOCKS5://p.proxy:1080", testLogger())
	if len(entries) != 1 || entries[0].Scheme != "socks5" {
		t.Errorf("Parse() = %+v, want a lowercase socks5 entry", entries)
	}
}

func TestEntryStringMasksPassword(t *testing.T) {
	e, err := parseEntry("socks5://user:secret@p.proxy:1080")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.String(); got != "socks5://user:***@p.proxy:1080" {
		t.Errorf("String() = %q, want the masked form", got)
	}

	plain, err := parseEntry("http://p.proxy:8080")
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.String(); got != "http://p.proxy:8080" {
		t.Errorf("String() = %q, want the raw form", got)
	}
}
