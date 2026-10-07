/* eslint-disable */
// Remember only the prefix actually revealed by prediction, using physical cards
// so two copies of the same definition cannot be mistaken for each other.
export function knownDeckTop(deck) {
  const known = deck.arenaKnownTop || [];
  const mismatch = known.findIndex((card, index) => deck.cards[index] !== card);
  return known.slice(0, mismatch < 0 ? known.length : mismatch);
}

export function revealDeckTop(deck) {
  deck.arenaKnownTop = deck.cards.slice(0, 2);
  return [...deck.arenaKnownTop];
}

export function installDeckOperations() {
  Deck.prototype.knownTopCards = function () {
    return knownDeckTop(this);
  };
  const remove = Deck.prototype.removeCard;
  Deck.prototype.removeCard = function (card, ...args) {
    const known = knownDeckTop(this);
    const index = typeof card === 'number' ? card : this.cards.indexOf(card);
    const result = remove.call(this, card, ...args);
    if (index >= 0 && index < known.length) known.splice(index, 1);
    this.arenaKnownTop = known;
    return result;
  };
  const randomInsert = Deck.prototype.addCardRandom;
  Deck.prototype.addCardRandom = function (card) {
    // Upstream insertion swaps a random existing card with the appended card.
    // The resulting order is hidden; do not peek at it to repair the memory.
    this.arenaKnownTop = [];
    return randomInsert.call(this, card);
  };
  const reset = Deck.prototype.reset;
  Deck.prototype.reset = function (...args) {
    this.arenaKnownTop = [];
    return reset.apply(this, args);
  };
  Deck.prototype.swapToBottom = function (container, card) {
    const index = container.cards.indexOf(card);
    if (index < 0) return;
    // Context Window is a replacement, not an extra resource draw. Leave the
    // upstream random mulligan unchanged and preserve the deck's visual stack.
    this.cards.push(container.removeCard(card));
    this.addCardElement();
    this.resize();
    container.addCard(this.removeCard(0), index);
  };
}
