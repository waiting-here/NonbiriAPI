import type { AppearancePort } from '../user/appearance/context';
import { cards, cardById, themes, tierOf, themeById } from '../user/activities/gacha/catalog';
import {
  defaultRules,
  drawOne,
  freshPity,
  poolOpen,
  randomInt,
} from '../user/activities/gacha/rules';
import type {
  Action,
  DrawReceipt,
  GachaPort,
  Outcome,
  Pool,
  Snapshot,
} from '../user/activities/gacha/types';

interface Saved {
  appearance?: string;
  moment?: string;
  state: Snapshot;
  entered: boolean;
  day: string;
  limitedDay?: string;
  month: string;
  receipts: Record<string, { input: string; result: Outcome }>;
  history: DrawReceipt[];
}
const storageKey = 'nb.gacha.local-preview.v2';
const now = () => Math.floor(Date.now() / 1000);
const day = () =>
  new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date());
const month = () => day().slice(0, 7);

function initial(): Saved {
  const at = now();
  const common: Pool = {
    id: 'permanent',
    name: '永续智能',
    name_en: 'Perpetual intelligence',
    cover: 'permanent',
    kind: 'permanent',
    group: 'permanent',
    rules: { ...defaultRules },
    featured: [],
    starts_at: null,
    ends_at: null,
    paused: false,
    published: true,
    revision: 1,
    rates: [0.51317544, 0.32656619, 0.13817568, 0.02208269],
  };
  return {
    entered: false,
    day: '',
    month: month(),
    receipts: {},
    history: [],
    state: {
      visible_themes: [...new Set(cards.map((card) => card.theme))],
      tickets: { permanent: 0, limited: 0 },
      fragments: { N: 0, R: 0, SR: 0, SSR: 0 },
      credits: { general: '10000000', game: '10000000' },
      owned: {},
      pity: { permanent: freshPity(defaultRules), featured: freshPity(defaultRules) },
      newcomer: true,
      checked_in: false,
      limited_checked_in: false,
      claimed: [],
      redeemed: [],
      month_used: { permanent: 0, limited: 0 },
      donation: '0',
      pools: [
        common,
        ...(['i', 'j', 'l', 'm'] as const).map((theme) => ({
          ...common,
          id: theme,
          name: themeById[theme].name,
          name_en: themeById[theme].name_en,
          cover: theme,
          kind: 'limited' as const,
          group: theme === 'l' || theme === 'm' ? 'special' : 'featured',
          rules:
            theme === 'l' || theme === 'm'
              ? { ...defaultRules, ssr_featured: 1500, ssr_guarantee_misses: 2 }
              : { ...defaultRules },
          starts_at: at - 3600,
          ends_at: at + 30 * 86400,
          featured: cards.filter((c) => c.theme === theme || c.theme === 'h').map((c) => c.id),
        })),
      ],
      shop: {
        permanent_price: '10000',
        limited_price: '10000',
        n_ticket: 20,
        r_ticket: 10,
        sr_ticket: 5,
        ssr_tickets: 1,
        permanent_cap: 20,
        limited_cap: 10,
        sr_card: 10,
        ssr_card: 3,
        sp_card: 9,
        chibi_n: 30,
        chibi_r: 15,
        revision: 1,
      },
      server_time: at,
      time_zone: 'UTC+08:00',
    },
  };
}
function read(): Saved {
  try {
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      const saved = JSON.parse(raw) as Saved;
      saved.state.visible_themes = [...new Set(cards.map((card) => card.theme))];
      return saved;
    }
  } catch {
    /* The preview remains available without browser storage. */
  }
  return initial();
}
let memory = read();
function save(value: Saved) {
  memory = value;
  try {
    localStorage.setItem(storageKey, JSON.stringify(value));
  } catch {
    /* Keep the current session usable. */
  }
}
function sync(data: Saved) {
  data.state.server_time = now();
  for (const pool of data.state.pools)
    pool.rates ??= [0.51317544, 0.32656619, 0.13817568, 0.02208269];
  data.state.checked_in = data.day === day();
  data.state.limited_checked_in = data.limitedDay === day();
  if (data.month !== month()) {
    data.month = month();
    data.state.month_used = { permanent: 0, limited: 0 };
  }
}
function fail(code: string): never {
  throw new Error(code);
}
function run(input: Action, key: string): Outcome {
  const data = structuredClone(memory);
  sync(data);
  const old = data.receipts[key];
  if (old)
    return old.input === JSON.stringify(input)
      ? { ...structuredClone(old.result), snapshot: structuredClone(data.state) }
      : fail('conflict');
  const state = data.state;
  let draw: DrawReceipt | undefined;
  switch (input.action) {
    case 'enter':
      if (!data.entered) {
        data.entered = true;
        state.tickets.permanent += 10;
      }
      break;
    case 'check-in':
      if (state.checked_in) fail('already-claimed');
      data.day = day();
      state.checked_in = true;
      state.tickets.permanent++;
      break;
    case 'limited-check-in':
      if (state.limited_checked_in) fail('already-claimed');
      data.limitedDay = day();
      state.limited_checked_in = true;
      state.tickets.limited++;
      break;
    case 'draw': {
      const pool = state.pools.find((p) => p.id === input.pool_id);
      if (!pool || !poolOpen(pool, now())) fail('pool-closed');
      if (state.tickets[pool.kind] < input.quantity) fail('tickets-insufficient');
      state.tickets[pool.kind] -= input.quantity;
      const welcome = pool.kind === 'permanent' && input.quantity === 10 && state.newcomer;
      draw = {
        id: crypto.randomUUID(),
        pool_id: pool.id,
        pool_name: pool.name,
        created_at: now(),
        items: [],
      };
      for (let i = 0; i < input.quantity; i++) {
        const forced =
          welcome && i === 9 && !draw.items.some((item) => cardById[item.card_id].rarity === 'SSR');
        const result = drawOne(
          pool,
          state.pity[pool.group] ?? freshPity(pool.rules),
          randomInt,
          forced,
        );
        state.pity[pool.group] = result.pity;
        result.item.duplicate = !!state.owned[result.item.card_id];
        if (result.item.duplicate) state.fragments[tierOf(cardById[result.item.card_id].rarity)]++;
        else state.owned[result.item.card_id] = { at: now(), source: 'draw' };
        draw.items.push(result.item);
      }
      if (welcome) state.newcomer = false;
      data.history.unshift(draw);
      data.history = data.history.filter((receipt) => receipt.created_at >= now() - 180 * 86400);
      break;
    }
    case 'buy': {
      if (input.revision !== state.shop.revision) fail('price-changed');
      if (!Number.isSafeInteger(input.quantity) || input.quantity < 1 || input.quantity > 100)
        fail('invalid');
      let cost =
        BigInt(state.shop[input.ticket === 'permanent' ? 'permanent_price' : 'limited_price']) *
        BigInt(input.quantity);
      let game = BigInt(state.credits.game),
        general = BigInt(state.credits.general);
      const available = general + (input.ticket === 'permanent' ? game : 0n);
      if (cost > available) fail('credits-insufficient');
      if (input.ticket === 'permanent') {
        const part = game < cost ? game : cost;
        game -= part;
        cost -= part;
      }
      general -= cost;
      state.credits = { game: String(game), general: String(general) };
      state.tickets[input.ticket] += input.quantity;
      break;
    }
    case 'redeem': {
      if (input.revision !== state.shop.revision) fail('price-changed');
      const offers: Record<
        string,
        {
          spend: Partial<typeof state.fragments>;
          ticket: 'permanent' | 'limited';
          quantity: number;
        }
      > = {
        'ticket-n': { spend: { N: state.shop.n_ticket }, ticket: 'permanent', quantity: 1 },
        'ticket-r': { spend: { R: state.shop.r_ticket }, ticket: 'permanent', quantity: 1 },
        'ticket-sr': { spend: { SR: state.shop.sr_ticket }, ticket: 'limited', quantity: 1 },
        'ticket-ssr': { spend: { SSR: state.shop.ssr_tickets }, ticket: 'limited', quantity: 2 },
      };
      const offer = offers[input.product],
        card = cardById[input.product];
      if (!offer && (!card || card.source === 'permanent')) fail('invalid');
      if (card && (state.owned[card.id] || state.redeemed.includes(card.id))) fail('already-owned');
      if (
        card?.source === 'limited' &&
        !state.pools.some((p) => poolOpen(p, now()) && p.featured.includes(card.id))
      )
        fail('pool-closed');
      const spend =
        offer?.spend ??
        (card.source === 'shop'
          ? { N: state.shop.chibi_n, R: state.shop.chibi_r }
          : {
              [tierOf(card.rarity)]:
                card.rarity === 'SR'
                  ? state.shop.sr_card
                  : card.rarity === 'SP'
                    ? state.shop.sp_card
                    : state.shop.ssr_card,
            });
      if (
        Object.entries(spend).some(
          ([rarity, count]) => state.fragments[rarity as keyof typeof state.fragments] < count,
        )
      )
        fail('fragments-insufficient');
      if (
        offer &&
        state.month_used[offer.ticket] + offer.quantity >
          state.shop[offer.ticket === 'permanent' ? 'permanent_cap' : 'limited_cap']
      )
        fail('monthly-limit');
      for (const [rarity, count] of Object.entries(spend))
        state.fragments[rarity as keyof typeof state.fragments] -= count;
      if (offer) {
        state.tickets[offer.ticket] += offer.quantity;
        state.month_used[offer.ticket] += offer.quantity;
      } else {
        state.owned[card.id] = { at: now(), source: 'shop' };
        state.redeemed.push(card.id);
      }
      break;
    }
    case 'claim': {
      const theme = themes.find((t) => t.id === input.theme);
      if (!theme || cards.filter((c) => c.theme === theme.id).some((c) => !state.owned[c.id]))
        fail('theme-incomplete');
      if (state.claimed.includes(theme.id)) fail('already-claimed');
      state.claimed.push(theme.id);
      state.tickets[theme.source === 'permanent' ? 'permanent' : 'limited']++;
      state.donation = String(BigInt(state.donation) + 10n);
      const asset = theme.source === 'permanent' ? 'game' : 'general';
      state.credits[asset] = String(BigInt(state.credits[asset]) + 10000n);
      break;
    }
  }
  const result = { snapshot: structuredClone(state), draw };
  data.receipts[key] = { input: JSON.stringify(input), result };
  // Preview-only replay memory is bounded; the server owns durable receipts.
  const keys = Object.keys(data.receipts);
  keys.slice(0, Math.max(0, keys.length - 30)).forEach((id) => delete data.receipts[id]);
  save(data);
  return result;
}
export const localPort: GachaPort = {
  scope: 'preview',
  async load() {
    sync(memory);
    return structuredClone(memory.state);
  },
  async act(input, key) {
    return run(input, key);
  },
  async history(cursor) {
    const offset = Number(cursor || 0);
    const all = memory.history.filter((item) => item.created_at > now() - 180 * 86400);
    return {
      items: structuredClone(all.slice(offset, offset + 50)),
      next_cursor: all.length > offset + 50 ? String(offset + 50) : undefined,
    };
  },
};
export function resetPreview(mode: 'new' | 'collection') {
  const data = initial();
  if (mode === 'collection') {
    data.entered = true;
    data.state.newcomer = false;
    data.state.tickets = { permanent: 100, limited: 100 };
    data.state.fragments = { N: 100, R: 100, SR: 50, SSR: 20 };
    for (const card of cards) data.state.owned[card.id] = { at: now(), source: 'draw' };
  }
  try {
    sessionStorage.removeItem('nb.gacha.pending:preview');
  } catch {
    /* Optional browser storage. */
  }
  save(data);
}

function appearanceState() {
  const unlocked = [
    'default',
    'heart-moment',
    ...cards
      .filter((card) => card.appearance && memory.state.owned[card.id])
      .map((card) => card.appearance!),
  ];
  return {
    selected: unlocked.includes(memory.appearance ?? '') ? memory.appearance! : 'default',
    unlocked,
    visible: ['default', 'heart-moment', 'celestial', 'wandering'],
    moment: memory.moment ?? 'cover:permanent',
    moments: [
      'cover:permanent',
      ...new Set(
        memory.state.pools
          .filter((pool) => pool.kind === 'limited' && pool.published)
          .map((pool) => 'cover:' + pool.cover),
      ),
      ...cards.filter((card) => memory.state.owned[card.id]).map((card) => 'card:' + card.id),
      ...themes
        .filter((theme) =>
          cards
            .filter((card) => card.theme === theme.id)
            .every((card) => memory.state.owned[card.id]),
        )
        .map((theme) => 'illustration:' + theme.id),
    ],
    updated_at: null,
  };
}
export const localAppearancePort: AppearancePort = {
  scope: 'preview',
  async load() {
    return appearanceState();
  },
  async select(selected, moment) {
    if (moment && !appearanceState().moments.includes(moment)) throw new Error('moment-locked');
    if (!appearanceState().unlocked.includes(selected)) throw new Error('appearance-locked');
    save({ ...memory, appearance: selected, moment: moment ?? memory.moment });
    return appearanceState();
  },
};

let previewAppearance = 'celestial';
export const appearancePreviewPort: AppearancePort = {
  scope: 'preview-appearance',
  async load() {
    return {
      selected: previewAppearance,
      unlocked: ['default', 'heart-moment', 'celestial', 'wandering'],
      moment: 'cover:permanent',
      moments: ['cover:permanent'],
      updated_at: null,
    };
  },
  async select(selected) {
    if (!['default', 'heart-moment', 'celestial', 'wandering'].includes(selected))
      throw new Error('appearance-locked');
    previewAppearance = selected;
    return this.load();
  },
};
