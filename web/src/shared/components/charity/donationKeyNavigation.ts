import { createContext } from 'react';
import type { CharityRole } from '@shared/operations/charity';

export const DonationKeyDetailRole = createContext<CharityRole | null>(null);

export function donationKeyDetailPath(
  role: CharityRole,
  source: { donation_id: string; donation_key_id: string },
) {
  const params = new URLSearchParams(role === 'steward' ? { tab: 'charity' } : {});
  params.set('charity_section', 'donations');
  params.set('donation_id', source.donation_id);
  params.set('donation_key', source.donation_key_id);
  return `${role === 'admin' ? '/charity' : '/steward'}?${params}`;
}
