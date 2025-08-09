export class HTTPError extends Error {
  private _code: number;

  constructor(message: string, code: number, options?: ErrorOptions) {
    super(message, options);

    this._code = code;
  }

  get code(): number {
    return this._code;
  }
}
