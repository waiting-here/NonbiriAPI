import type { Snapshot } from './types';

export function shardTicketOffers(shop: Snapshot['shop']) {
  return [
    { id: 'ticket-n', rarity: 'N', cost: BigInt(shop.n_ticket), quantity: 1, ticket: 'permanent' },
    { id: 'ticket-r', rarity: 'R', cost: BigInt(shop.r_ticket), quantity: 1, ticket: 'permanent' },
    { id: 'ticket-sr', rarity: 'SR', cost: BigInt(shop.sr_ticket), quantity: 1, ticket: 'limited' },
    {
      id: 'ticket-ssr',
      rarity: 'SSR',
      cost: BigInt(shop.ssr_tickets),
      quantity: 2,
      ticket: 'limited',
    },
  ] as const;
}
