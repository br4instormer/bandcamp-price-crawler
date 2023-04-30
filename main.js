const { readFile } = require("node:fs/promises");
const http = require("node:http");
const https = require("node:https");
const queue = require("async/queue");
const cheerio = require("cheerio");
const axios = require("axios");
const argv = require("./argv.js");
const options = require("./options.json");

const State = {
  "NOT_ABLE_TO_DOWNLOAD": 1,
  "FREE_DOWNLOAD": 2,
  "NAME_YOUR_PRICE": 3,
};
const Message = {
  [State.NOT_ABLE_TO_DOWNLOAD]: "Not available to download",
  [State.FREE_DOWNLOAD]: "Free download",
  [State.NAME_YOUR_PRICE]: "Name your price",
};
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
const printCost = async ({ client, url }) => {
  const cost = getCost(cheerio.load(await getBody(client, url)));
  const isFree = cost === Message.FREE_DOWNLOAD;

  if (!SHOW_ONLY_FREE) {
    println(cost, url);
    return;
  }
  
  if (isFree) {
    println(cost, url);
  }
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
    return Message[State.NOT_ABLE_TO_DOWNLOAD];
  }

  if (isFreeDownload) {
    return Message[State.FREE_DOWNLOAD];
  }

  if (isNameYourPrice) {
    return Message[State.NAME_YOUR_PRICE];
  }

  return parseCostDownload($);
}

async function main(
  urlfile,
  client,
  q,
) {
  parseArray(await urlfile)
    .forEach((url) => q.push({ client, url }));

  await q.drain();
}

main(
  readUrlFile(URL_FILEPATH),
  axios.create(clientOptions),
  queue(printCost, MAX_CONCURENT_DOWNLOADS),
);