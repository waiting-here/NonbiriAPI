import { webcrypto } from 'node:crypto';
import { screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { RandomnessProof } from './RandomnessProof';

const proof = {
  algorithm: 'hmac-sha256-reject64-v1',
  game: 'blackjack',
  resource_id: 'bjt_AAAAAAAAAAAAAAAAAAAAAA',
  rules: 'six-decks-s17-v1',
  commitment: '5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260',
  seed: '2a'.repeat(32),
  streams: [{ label: 'shoe', samples: 'AAAAAAAAATgAAAAAAAABBg==' }],
};
afterEach(() => vi.unstubAllGlobals());
const install = (p: unknown) => {
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify({ proof: p }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    ),
  );
};
it('shows an opening commitment without inventing a disclosed seed or a verification button', async () => {
  const opening = { ...proof, seed: undefined, streams: undefined };
  install(opening);
  const view = await renderWithProviders(
    <RandomnessProof game="blackjack" id={proof.resource_id} />,
    { station: 'user', role: 'user' },
  );
  await waitFor(() => expect(screen.getByText('Opening commitment locked')).toBeInTheDocument());
  await view.user.click(screen.getByText('Verify randomness'));
  expect(screen.getByRole('button', { name: 'Save commitment' })).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Verify locally' })).toBeNull();
  expect(screen.queryByText(proof.seed)).toBeNull();
});
it('verifies a disclosed proof locally and offers its download', async () => {
  install(proof);
  const view = await renderWithProviders(
    <RandomnessProof game="blackjack" id={proof.resource_id} terminal />,
    { station: 'user', role: 'user' },
  );
  await waitFor(() => expect(screen.getByText('Seed disclosed')).toBeInTheDocument());
  await view.user.click(screen.getByText('Verify randomness'));
  await view.user.click(screen.getByRole('button', { name: 'Verify locally' }));
  expect(await screen.findByText(/all 1 recorded draws reproduce/)).toBeVisible();
  expect(screen.getByRole('button', { name: 'Download proof' })).toBeVisible();
});
it('labels retained legacy games without fabricating a proof', async () => {
  install(null);
  const view = await renderWithProviders(
    <RandomnessProof game="blackjack" id={proof.resource_id} terminal />,
    { station: 'user', role: 'user' },
  );
  await view.user.click(screen.getByText('Verify randomness'));
  expect(await screen.findByText(/predates random proofs/)).toBeVisible();
});

it('clearly reports a seed mismatch without displaying a verified result', async () => {
  install({ ...proof, seed: '00'.repeat(32) });
  const view = await renderWithProviders(
    <RandomnessProof game="blackjack" id={proof.resource_id} terminal />,
    { station: 'user', role: 'user' },
  );
  await waitFor(() => expect(screen.getByText('Seed disclosed')).toBeInTheDocument());
  await view.user.click(screen.getByText('Verify randomness'));
  await view.user.click(screen.getByRole('button', { name: 'Verify locally' }));
  expect(await screen.findByText(/Verification failed:/)).toBeVisible();
  expect(screen.queryByText(/recorded draws reproduce/)).toBeNull();
});
