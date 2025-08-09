import { type State } from "./cost";

export default class Count {
  private readonly _items: Record<State, number> = {
    0: 0,
    1: 0,
    2: 0,
    3: 0,
  };

  public add(state: State): void {
    this._items[state] += 1;
  }

  public get(state: State): number {
    return this._items[state];
  }
}
