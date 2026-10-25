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
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `PROGRESS` | `auto` | Live task table: `auto` (on when stdout is a terminal), `on`, `off` |

On HTTP 429 the retry pause grows exponentially (`RETRY_BASE_DELAY_MS * 2^attempt`, ±20% jitter, capped by `RETRY_MAX_DELAY_MS`, a larger `Retry-After` header wins) and the global pause multiplier doubles, so the pauses between all requests widen until the server recovers.

### Using

```bash
make run ARGS="-i some/path/urls.txt"
make run ARGS="-f"     # show only free download albums
./bin/crawler --help
```

While running in a terminal, a full-screen Bubble Tea dashboard shows a scrollable results table (every processed page with its URL and price, new rows stream in at the bottom while the view follows the tail; scroll with ↑/↓, `G` jumps back to the latest) with the overall progress bar (processed/total, elapsed, ETA) beneath it; failed fetches land in the warnings tail; `ctrl+c` stops the crawl gracefully. `PROGRESS=on|off` forces the dashboard on or off (default `auto`: enabled only when stdout is a terminal).

## SonarQube

The analysis is sent through `sonar-scanner` running in Docker (requires a reachable SonarQube server and a token with "Execute Analysis" permission):

```bash
export SONAR_HOST_URL=https://sonar.example.com
export SONAR_TOKEN=your-token
make sonar           # runs tests with coverage, then the scanner
```
