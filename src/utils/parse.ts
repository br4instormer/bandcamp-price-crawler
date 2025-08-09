import { type CheerioAPI } from "cheerio";
import { Cost, State } from "../cost";

export const parseDigitalDownload = ($: CheerioAPI): boolean =>
  $(".buyItem.digital").get(0) !== undefined;
export const parseFreeDownload = ($: CheerioAPI): boolean =>
  $(".buyItem.digital .compound-button .download-link").text().trim() === "Free Download";
export const parseNameYourPrice = ($: CheerioAPI): boolean =>
  $(".buyItem.digital .buyItemNyp").text().trim() === "name your price";
export const parseCostDownload = ($: CheerioAPI): string =>
  $(".buyItem.digital .nobreak").find(".base-text-color").text().trim();

export function parseCost($: CheerioAPI): Cost {
  const hasDigitalDownload = parseDigitalDownload($);
  const isFreeDownload = parseFreeDownload($);
  const isNameYourPrice = parseNameYourPrice($);

  if (!hasDigitalDownload) {
    return new Cost(State.NOT_ABLE_TO_DOWNLOAD);
  }

  if (isFreeDownload) {
    return new Cost(State.FREE_DOWNLOAD);
  }

  if (isNameYourPrice) {
    return new Cost(State.NAME_YOUR_PRICE);
  }

  return new Cost(State.REGULAR_DOWNLOAD, parseCostDownload($));
}
