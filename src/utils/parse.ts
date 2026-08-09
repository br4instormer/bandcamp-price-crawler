import { type CheerioAPI } from "cheerio";
import { Cost, State } from "../cost";

const parseDigitalDownload = ($: CheerioAPI): boolean => $(".buyItem.digital").get(0) !== undefined;
const parseFreeDownload = ($: CheerioAPI): boolean =>
  $(".buyItem.digital .compound-button .download-link").text().trim() === "Free Download";
const parseNameYourPrice = ($: CheerioAPI): boolean =>
  $(".buyItem.digital .buyItemNyp").text().trim() === "name your price";
const parseCostDownload = ($: CheerioAPI): string =>
  $(".buyItem.digital .nobreak").find(".base-text-color").text().trim();
const hasCurrencySign = (s: string): boolean => !/^[0-9.]+$/.test(s);
const parseCurrencyCode = ($: CheerioAPI): string | undefined =>
  $($(".buyItem.digital .nobreak .secondaryText").get(0))?.text().trim();
const parseCostWCurrency = ($: CheerioAPI): string => {
  const cost = parseCostDownload($);

  return hasCurrencySign(cost) ? cost : `${cost} ${parseCurrencyCode($) ?? ""}`;
};

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

  return new Cost(State.REGULAR_DOWNLOAD, parseCostWCurrency($));
}
