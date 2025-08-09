import { AxiosError, type AxiosInstance } from "axios";
import { HTTPError } from "../errors/http_error";

export const fetchPage = async (client: AxiosInstance, url: string): Promise<string | Error> =>
  client
    .get(url)
    .then(({ data }) => data)
    .catch((error?: AxiosError) =>
      error?.response ? new HTTPError(error.response.statusText, error.response.status) : error,
    );
