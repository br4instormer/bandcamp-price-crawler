const { State } = require("./cost.js");

class Count {
  constructor() {
    this._items = Object.values(State).reduce((result, item) => (result[item] = 0, result), {});
  }

  add(state) {
    this._items[state] += 1;
  }

  get(state) {
    return this._items[state];
  }
}

module.exports = Count;