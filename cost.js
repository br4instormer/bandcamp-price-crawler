class Cost {
  constructor(state, price = undefined) {
    this._state = state;
    this._price = Message[state] || price;
  }

  get price() {
    return this._price;
  }

  get state() {
    return this._state;
  }

  isFree() {
    return this._state === State.FREE_DOWNLOAD;
  }
}

const State = {
  "NOT_ABLE_TO_DOWNLOAD": 1,
  "FREE_DOWNLOAD": 2,
  "NAME_YOUR_PRICE": 3,
  "REGULAR_DOWNLOAD": 4,
};

const Message = {
  [State.NOT_ABLE_TO_DOWNLOAD]: "Not available to download",
  [State.FREE_DOWNLOAD]: "Free download",
  [State.NAME_YOUR_PRICE]: "Name your price",
};

module.exports = {
  Cost,
  State,
  Message,
}