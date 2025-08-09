import axios, { AxiosInstance, CreateAxiosDefaults } from "axios";
import axiosRetry, { IAxiosRetryConfig } from "axios-retry";
export { axiosRetry };

export const createClient = (options: CreateAxiosDefaults): AxiosInstance => axios.create(options);
export const retryClient = (instance: AxiosInstance, options: IAxiosRetryConfig): AxiosInstance => (
  axiosRetry(instance, options),
  instance
);
