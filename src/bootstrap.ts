import { createClient, retryClient, axiosRetry } from "./client";
import Logger from "./log";
import Config from "./config";

export const config = new Config();
export const log = new Logger();
export const client = retryClient(
  createClient({
    responseType: "document",
    headers: {
      "Accept-Encoding": "gzip, deflate, br",
      "User-Agent":
        "Mozilla/5.0 (X11; Datanyze; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/65.0.3325.181 Safari/537.36",
    },
  }),
  {
    retries: 50,
    shouldResetTimeout: true,
    retryDelay: axiosRetry.exponentialDelay,
    retryCondition(error) {
      return (
        axiosRetry.isNetworkError(error) ||
        axiosRetry.isRetryableError(error) ||
        error.status === 429
      );
    },
  },
);
