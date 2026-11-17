module.exports = {
  discount(amount) {
    return amount >= 1000 ? 0.9 : 1;
  },
};
