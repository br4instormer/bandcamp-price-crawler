const { readFile } = require("node:fs/promises");
const { setTimeout: sleep } = require("node:timers/promises");
const http = require("node:http");
const https = require("node:https");
const queue = require("async/queue");
const cheerio = require("cheerio");
const axios = require("axios");
const argv = require("./argv.js");
const { Cost, State } = require("./cost.js");
const options = require("./options.json");
const Count = require("./count.js");

const clientOptions = {
  ...options,
  httpAgent: new http.Agent({ keepAlive: true }),
  httpsAgent: new https.Agent({ keepAlive: true }),
};
const MAX_CONCURENT_DOWNLOADS = 5;
const SHOW_ONLY_FREE = argv.free;
const URL_FILEPATH = argv.include;

const println = (cost, url) => console.log(`[${cost}] ${url}`);
const parseArray = (string) => string.split("\n");
const readUrlFile = async (filename) => readFile(filename, { encoding: "utf8" });
const printCost = async ({ client, url, count }) => {
  const cost = getCost(cheerio.load(await getBody(client, url)));
  const isFree = cost.isFree();

  count.add(cost.state);

  if (!SHOW_ONLY_FREE) {
    println(cost.price, url);

    await sleep(500);

    return;
  }
  
  if (isFree) {
    println(cost.price, url);
  }

  await sleep(500);
};
const getBody = async (client, url) => client.get(url).then(({ data }) => data);
const parseDigitalDownload = ($) => $(".buyItem.digital").get(0) !== undefined;
const parseFreeDownload = ($) => $(".buyItem.digital .compound-button .download-link").text().trim() === "Free Download";
const parseNameYourPrice = ($) => $(".buyItem.digital .buyItemNyp").text().trim() === "name your price";
const parseCostDownload = ($) => $(".buyItem.digital .nobreak").find(".base-text-color").text().trim();

function getCost($) {
  const hasDigitalDownload = parseDigitalDownload($);
  const isFreeDownload = parseFreeDownload($);
  const isNameYourPrice = parseNameYourPrice($);

  if (!hasDigitalDownload) {
    return new Cost(State.NOT_ABLE_TO_DOWNLOAD)
  }

  if (isFreeDownload) {
    return new Cost(State.FREE_DOWNLOAD);
  }

  if (isNameYourPrice) {
    return new Cost(State.NAME_YOUR_PRICE);
  }

  return new Cost(State.REGULAR_DOWNLOAD, parseCostDownload($));
}

async function main(
  urlfile,
  client,
  q,
  count,
) {
  parseArray(await urlfile)
    .forEach((url) => q.push({ client, url, count }));

  await q.drain();

  if (SHOW_ONLY_FREE && !count.get(State.FREE_DOWNLOAD)) {
    console.warn("No free download albums at all!");
  }
}

main(
  readUrlFile(URL_FILEPATH),
  axios.create(clientOptions),
  queue(printCost, MAX_CONCURENT_DOWNLOADS),
  new Count,
);