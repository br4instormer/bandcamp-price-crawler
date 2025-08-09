# Bandcamp price crawler

The application visits pages given in `urls.txt` to find price of product and print to stdout

## Install & Using

Node.js v22.15

### Install

```bash
pnpm i
pnpm build
pnpm prune --prod

cp .env.example .env
```

### Using

```bash
pnpm run help
```

```bash
pnpm start -- -i some/path/urls.txt
pnpm start -- -f

Options:
  -i, --include  File contains bandcamp urls                  [string] [default:
             "/home/user/bandcamp-price-crawler/urls.txt"]
  -f, --free     Show only free download albums       [boolean] [default: false]
      --help     Show help                                             [boolean]
```
