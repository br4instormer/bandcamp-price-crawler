# Bandcamp price crawler (Go)

The application visits pages given in `urls.txt` to find price of product and print to stdout.

Go port of the original TypeScript crawler: worker-pool queue, in-flight request limiter, random pauses before requests and exponential backoff with 429 handling.

## Install & Using

Go 1.27

### Build

```bash
make build        # builds bin/crawler
make test         # unit tests
make race         # unit tests with the race detector
make coverage     # tests + coverage.out
```

### Configure

Parameters are read from the environment; an optional `.env` file is loaded first (real environment variables always win). See `.env.example`:

| Variable | Default | Meaning |
|---|---|---|
| `CONCURENT_DOWNLOADS` | `5` | Queue worker pool size |
| `DELAY_MIN` / `DELAY_MAX` | `300` / `700` | Bounds (ms) of the random pause before each request |
| `MAX_CONCURRENT_REQUESTS` | = workers | Cap on simultaneous HTTP requests |
| `RETRIES` | `50` | Retries for 429 / 5xx / network errors |
| `RETRY_BASE_DELAY_MS` / `RETRY_MAX_DELAY_MS` | `1000` / `60000` | Exponential retry delay bounds |
| `HTTP_TIMEOUT_MS` | `30000` | Per-request timeout |
| `USER_AGENT` | Chrome-ish UA | `User-Agent` header |
| `HTTP_PROXIES` | *(empty)* | Proxy pool: inline list or a path to a list file, see below |
| `PROXY_ERROR_BUDGET` | `5` | Consecutive failed attempts through one proxy before it is blocked |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `PROGRESS` | `auto` | Live task table: `auto` (on when stdout is a terminal), `on`, `off` |

On HTTP 429 the retry pause grows exponentially (`RETRY_BASE_DELAY_MS * 2^attempt`, ±20% jitter, capped by `RETRY_MAX_DELAY_MS`, a larger `Retry-After` header wins) and the global pause multiplier doubles, so the pauses between all requests widen until the server recovers.

### Proxies

`HTTP_PROXIES` accepts two notations plus the empty value:

```bash
# 1. an inline comma-separated list (http, https, socks5, socks5h;
#    user:password@ for basic auth / SOCKS5 credentials)
HTTP_PROXIES=https://proxy.server:8080,socks5://socks5.proxies:1080,socks5://user:password@authenticated.proxy:1080

# 2. a path to a file with one proxy per line (# comments and blank lines are ignored)
HTTP_PROXIES=/var/tmp/proxies.txt

# 3. empty (or unset) — no proxies, direct connection
HTTP_PROXIES=
```

A proxy without an explicit port falls back to the scheme default (80 / 443 / 1080). Invalid entries are dropped, the rest still serve. Every request takes the next proxy in round-robin order; a proxy that fails `PROXY_ERROR_BUDGET` times **in a row** (transport errors, 429 or 5xx — an ordinary 404 does not count) is blocked until the run restarts (`r`). When every proxy is blocked, the crawler falls back to the direct connection. The dashboard shows `proxies: N (blocked: M)` next to the progress header whenever proxies are configured.

The transport is [fasthttp](https://github.com/valyala/fasthttp); every proxy gets its own connection pool.

On HTTP 429 the retry pause grows exponentially (`RETRY_BASE_DELAY_MS * 2^attempt`, ±20% jitter, capped by `RETRY_MAX_DELAY_MS`, a larger `Retry-After` header wins) and the global pause multiplier doubles, so the pauses between all requests widen until the server recovers.

### Using

```bash
make run ARGS="-i some/path/urls.txt"
make run ARGS="-f"     # show only free download albums
./bin/crawler --help
```

While running in a terminal, a full-screen Bubble Tea dashboard shows a scrollable results table (every processed page with its URL and price, new rows stream in at the bottom while the view follows the tail; scroll with ↑/↓, `G` jumps back to the latest) with the overall progress bar (processed/total, elapsed, ETA) beneath it; failed fetches land in the warnings tail. When proxies are configured, the header shows `proxies: N (blocked: M)` — the pool size and how many proxies are currently blocked by the error budget. `space` pauses the crawl while it is running — a `⏸ paused` marker replaces the spinner in the header and the legend offers `space — resume`; urls not started yet wait on the gate and already in-flight ones finish, so the pause lands between urls; after every url is processed (or once a stop was requested) the key becomes inert. `r` restarts the crawl from the very beginning — the key works only while the crawl is paused or already finished, aborts the current run and resets the dashboard (counters, timer, results and warnings start over, blocked proxies unblock); mid-run the key is inert. `ctrl+c` stops the crawl gracefully. When every url is processed, a note *"✓ all sources processed"* appears under the table and the process stays alive so the results remain readable — finish with `ctrl+c`, `q` or `esc`. `PROGRESS=on|off` forces the dashboard on or off (default `auto`: enabled only when stdout is a terminal).

## SonarQube

The analysis is sent through `sonar-scanner` running in Docker (requires a reachable SonarQube server and a token with "Execute Analysis" permission):

```bash
export SONAR_HOST_URL=https://sonar.example.com
export SONAR_TOKEN=your-token
make sonar           # runs tests with coverage, then the scanner
```
