import { QueueObject } from "async";
import { AxiosInstance } from "axios";
import Count from "./count";

export interface QPayload {
  client: AxiosInstance;
  url: string;
  count: Count;
}

export type Queue = QueueObject<QPayload>;
