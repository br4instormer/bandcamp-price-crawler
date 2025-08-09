import { DotenvFlowParseResult, DotenvFlowConfigResult, config } from "dotenv-flow";

export default class Config {
  private readonly config: DotenvFlowParseResult;

  constructor() {
    const result: DotenvFlowConfigResult = config({
      node_env: process.env.NODE_ENV,
      default_node_env: "development",
    });

    if (!result.parsed) {
      if (result.error) {
        console.error(result.error.message);
      }

      return;
    }

    this.config = result.parsed;
  }

  public get(key: string): string {
    return this.config[key] ?? "";
  }
}
