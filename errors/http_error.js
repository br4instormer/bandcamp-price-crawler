class HTTPError extends Error {
  #code;

  constructor(message, code, options) {
    super(message, options);

    this.#code = code;
  }

  get code() {
    return this.#code;
  }
}

module.exports = {
  HTTPError,
};
