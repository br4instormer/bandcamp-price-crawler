import { join, dirname } from "node:path";
import yargs from "yargs";
import { hideBin } from "yargs/helpers";

export default yargs(hideBin(process.argv))
  .usage("npm start -- -i some/path/urls.txt")
  .usage("npm start -- -f")
  .options({
    include: {
      alias: "i",
      default: join(dirname(""), "urls.txt"),
      description: "File contains bandcamp urls",
      type: "string",
    },
    free: {
      alias: "f",
      default: false,
      description: "Show only free download albums",
      type: "boolean",
    },
  })
  .version(false)
  .help("help")
  .parseSync();
