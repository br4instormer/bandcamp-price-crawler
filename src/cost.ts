export class Cost {
  private readonly _state: State;
  private readonly _price: string;

  constructor(state: State, price?: string) {
    this._state = state;
    this._price = price ?? Message[state];
  }

  public get price(): string {
    return this._price;
  }

  public get state(): State {
    return this._state;
  }

  public isFree(): boolean {
    return this._state === State.FREE_DOWNLOAD;
  }
}

export const enum State {
  NOT_ABLE_TO_DOWNLOAD,
  FREE_DOWNLOAD,
  NAME_YOUR_PRICE,
  REGULAR_DOWNLOAD,
}

export const Message: Record<State, string> = {
  [State.NOT_ABLE_TO_DOWNLOAD]: "Not available to download",
  [State.FREE_DOWNLOAD]: "Free download",
  [State.NAME_YOUR_PRICE]: "Name your price",
  [State.REGULAR_DOWNLOAD]: "",
};
