import { readFileSync } from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";
import { queue } from "async";
import { type AxiosInstance } from "axios";
import * as cheerio from "cheerio";
import argv from "./argv";
import { State } from "./cost";
import Count from "./count";
import { client, config, log } from "./bootstrap";
import type { QPayload, Queue } from "./types";
import { fetchPage, parseCost } from "./utils";

const CONCURENT_DOWNLOADS = Number(config.get("CONCURENT_DOWNLOADS"));
const SHOW_ONLY_FREE = argv.free;
const URL_FILEPATH = argv.include;

const worker = async (payload: QPayload): Promise<void> => {
  const { client, url, count } = payload;
  const result = await fetchPage(client, url);

  if (result instanceof Error) {
    log.warn(`Cannot get ${url} because of ${result.message}`);

    return;
  }

  const cost = parseCost(cheerio.load(result));
  const isFree = cost.isFree();
  const isDisplay = isFree || !SHOW_ONLY_FREE;

  count.add(cost.state);

  if (isDisplay) {
    log.log(`[${cost.price}] ${url}`);
  }

  await sleep(500);
};

((list: string, client: AxiosInstance, q: Queue, count: Count): void => {
  list
    .split("\n")
    .filter(Boolean)
    .forEach((url) => q.push({ client, url, count }));

  if (q.running()) {
    q.drain().then(() => {
      if (SHOW_ONLY_FREE && !count.get(State.FREE_DOWNLOAD)) {
        log.warn("No free download albums at all!");
      }
    });
  }
})(
  readFileSync(URL_FILEPATH, { encoding: "utf8" }),
  client,
  queue(worker, CONCURENT_DOWNLOADS),
  new Count(),
);
