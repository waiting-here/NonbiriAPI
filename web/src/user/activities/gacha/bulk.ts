import type { Ticket } from './types';

export const bulkTicketPrice = '10000';
export const bulkTicketQuantity = 10;
export const bulkTicketOffers = [
  { id: 'bulk-permanent', ticket: 'permanent' },
  { id: 'bulk-limited', ticket: 'limited' },
] as const satisfies readonly { id: string; ticket: Ticket }[];

export function isBulkProduct(product: string) {
  return bulkTicketOffers.some((offer) => offer.id === product);
}
