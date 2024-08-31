# Bandcamp price crawler

The application visits pages given in `urls.txt` to find price of product and print to stdout

## Install & Using

Node.js v20.15

### Install

```bash
npm i
```

### Using

```bash
npm run help
```

```bash
npm start -- -i some/path/urls.txt
npm start -- -f

Options:
  -i, --include  File contains bandcamp urls                  [string] [default:
             "/home/user/bandcamp-price-crawler/urls.txt"]
  -f, --free     Show only free download albums       [boolean] [default: false]
      --help     Show help                                             [boolean]
```
