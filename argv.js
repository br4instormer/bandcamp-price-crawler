const { join } = require("node:path");

const URL_FILENAME = "urls.txt";

module.exports = require("yargs")
  .usage("npm start -- -i some/path/urls.txt")
  .usage("npm start -- -f")
  .options({
    "include": {
      alias: "i",
      default: join(__dirname, URL_FILENAME),
      description: "File contains bandcamp urls",
      type: "string",
    },
    "free": {
      alias: "f",
      default: false,
      description: "Show only free download albums",
      type: "boolean",
    },
  })
  .version(false)
  .help("help")
  .argv;
