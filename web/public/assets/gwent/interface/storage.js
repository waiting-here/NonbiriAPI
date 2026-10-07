export function deckStorage(accountID, backing) {
  const memory = new Map(),
    prefix = accountID ? `nonbiri.gwent.decks.${accountID}.` : null;
  return {
    getItem(key) {
      try {
        return prefix ? backing.getItem(prefix + key) : (memory.get(key) ?? null);
      } catch {
        return memory.get(key) ?? null;
      }
    },
    setItem(key, value) {
      if (prefix) backing.setItem(prefix + key, String(value));
      memory.set(key, String(value));
    },
    removeItem(key) {
      if (prefix) backing.removeItem(prefix + key);
      memory.delete(key);
    },
  };
}
