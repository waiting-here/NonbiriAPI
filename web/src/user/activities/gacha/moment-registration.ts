import { useEffect, useState } from 'react';
import type { MomentRegistration } from './moment-frame';

type Registry = Record<string, MomentRegistration>;
let loaded: Registry | undefined;
let pending: Promise<Registry> | undefined;

function loadRegistry() {
  if (loaded) return Promise.resolve(loaded);
  if (pending) return pending;
  pending = fetch('/assets/gacha/composition.json')
    .then(async (response) => {
      if (!response.ok) throw new Error('moment-registration-unavailable');
      const document = (await response.json()) as {
        cards?: Record<string, { moment_layers?: MomentRegistration }>;
      };
      const entries: Registry = {};
      for (const [id, card] of Object.entries(document.cards ?? {})) {
        if (card.moment_layers?.expansion || card.moment_layers?.scene)
          entries[id] = card.moment_layers;
      }
      loaded = entries;
      return entries;
    })
    .finally(() => {
      pending = undefined;
    });
  return pending;
}

/** One public artwork manifest per page; missing metadata retains the original artwork. */
export function useMomentExpansion(id?: string) {
  const [registry, setRegistry] = useState(loaded);
  useEffect(() => {
    if (!id) return;
    let current = true;
    void loadRegistry()
      .then((entries) => {
        if (current) setRegistry(entries);
      })
      .catch(() => {
        if (current) setRegistry({});
      });
    return () => {
      current = false;
    };
  }, [id]);
  return { ...((id && registry?.[id]) || {}), ready: !id || registry !== undefined };
}
