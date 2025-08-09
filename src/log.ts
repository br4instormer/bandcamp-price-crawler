import { Logger, type ILogObj } from "tslog";

export default class {
  public readonly _logger: Logger<ILogObj> = new Logger<ILogObj>({
    prettyLogTemplate: "{{hh}}:{{MM}}:{{ss}} {{logLevelName}} ",
    prettyErrorTemplate: "{{errorName}} {{errorMessage}}",
    prettyLogTimeZone: "local",
    minLevel: process.env["VERBOSE"] === "1" ? 0 : 3,
  });

  public log(...args: unknown[]): void {
    this._logger.info(...args);
  }

  public info(...args: unknown[]): void {
    this._logger.info(...args);
  }

  public error(...args: unknown[]): void {
    this._logger.error(...args);
  }

  public warn(...args: unknown[]): void {
    this._logger.warn(...args);
  }

  public debug(...args: unknown[]): void {
    this._logger.debug(...args);
  }
}
