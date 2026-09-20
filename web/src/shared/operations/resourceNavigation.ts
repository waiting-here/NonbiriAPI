interface ResourceNavigation {
  epoch: number;
  scroll: Map<string, number>;
}
const navigation = new WeakMap<object, ResourceNavigation>();
const epochKey = 'nonbiri:resource-navigation-epoch:v1';

function sessionEpoch(): number {
  try {
    const value = Number(sessionStorage.getItem(epochKey) ?? '0');
    return Number.isSafeInteger(value) && value >= 0 ? value : 0;
  } catch {
    return 0;
  }
}

export function resourceNavigation(client: object): ResourceNavigation {
  let state = navigation.get(client);
  if (!state) {
    state = { epoch: sessionEpoch(), scroll: new Map() };
    navigation.set(client, state);
  }
  return state;
}

export function clearResourceNavigation(client: object): void {
  const state = resourceNavigation(client);
  state.epoch += 1;
  state.scroll.clear();
  try {
    sessionStorage.setItem(epochKey, String(state.epoch));
  } catch {
    /* Memory scope still closes. */
  }
}
