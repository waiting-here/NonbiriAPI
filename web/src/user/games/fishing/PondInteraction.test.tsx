import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import {
  FishingGiftSource,
  FishingPondBoard,
  FishingPondDisclosure,
  FishingPondReturn,
} from './PondInteraction';
import type { FishingOutcome } from './types';

const batchID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
function catchItem(speciesKey = 'boot'): FishingOutcome {
  return {
    ordinal: 0,
    speciesKey,
    tier: 'junk',
    sizeCM: 0,
    reward: '0',
    netReward: '0',
    rake: { platform: '0', welfare: '0', thursday: '0' },
    blueFatFishLengthCM: null,
    milkCrabLengthCM: null,
    pond: { returnState: 'available', gift: null },
  };
}
const receipt = {
  batch_id: batchID,
  ordinal: 0,
  return_state: 'returned',
  returned_at: 1_800_000_000,
};
const path = `/api/games/fishing/batches/${batchID}/outcomes/0/return`;
const options = { station: 'user', route: '/games/fishing', role: 'user' } as const;

describe('Pond catch interaction', () => {
  it('discloses the persistent signature and gates new junk and fry actions on current anonymity', async () => {
    const onReceipt = vi.fn();
    const junk = catchItem();
    const fry = catchItem('fry');
    const content = (isPublic: boolean) => (
      <>
        <FishingPondDisclosure
          outcomes={[junk, { ...junk, ordinal: 2 }, fry]}
          isPublic={isPublic}
        />
        <FishingPondReturn
          batchID={batchID}
          outcome={junk}
          isPublic={isPublic}
          onReceipt={onReceipt}
        />
        <FishingPondReturn
          batchID={batchID}
          outcome={fry}
          isPublic={isPublic}
          onReceipt={onReceipt}
        />
      </>
    );
    const rendered = await renderWithProviders(content(false), options);
    expect(screen.queryByText(/The signature stays/)).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(
      screen.getByText('Turn off game anonymity before returning or releasing a catch.'),
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Return to pond' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Release' })).toBeDisabled();
    expect(screen.getByText(/does not add a gift or count/)).toBeVisible();
    rendered.rerender(content(true));
    expect(screen.getAllByText(/The signature stays/)).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Return to pond' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Release' })).toBeEnabled();
  });

  it('recovers an unknown return with the same resource, even after anonymity is enabled', async () => {
    const onReceipt = vi.fn();
    const fetchMock = installJsonFetchFixtures([{ method: 'POST', path, body: receipt }]);
    const fixtureFetch = fetchMock.getMockImplementation()!;
    let sends = 0;
    fetchMock.mockImplementation(async (input, init) => {
      sends += 1;
      if (sends === 1) throw new TypeError('Connection lost after commit');
      return fixtureFetch(input, init);
    });
    const outcome = catchItem();
    const rendered = await renderWithProviders(
      <FishingPondReturn batchID={batchID} outcome={outcome} isPublic onReceipt={onReceipt} />,
      options,
    );
    const send = screen.getByRole('button', { name: 'Return to pond' });
    fireEvent.click(send);
    fireEvent.click(send);
    await screen.findByText(/result is unconfirmed/);
    expect(sends).toBe(1);
    rendered.rerender(
      <FishingPondReturn
        batchID={batchID}
        outcome={outcome}
        isPublic={false}
        onReceipt={onReceipt}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry this catch' }));
    await screen.findByText('Returned to pond');
    expect(onReceipt).toHaveBeenCalledWith({
      batchID,
      ordinal: 0,
      returnState: 'returned',
      returnedAt: receipt.returned_at,
    });
    expect(sends).toBe(2);
    for (const [input, init] of fetchMock.mock.calls) {
      expect(String(input)).toBe(path);
      expect(init?.body).toBeUndefined();
      expect(new Headers(init?.headers).get('Idempotency-Key')).toBeNull();
    }
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('shows a gift signature without a return action, and leaves old catches ineligible', async () => {
    const gift = {
      ...catchItem('old_tire'),
      pond: {
        returnState: 'gift' as const,
        gift: {
          kind: 'public' as const,
          displayName: 'Long donor '.repeat(10),
          avatarURL: 'https://cdn.discordapp.com/avatars/a/b.png',
        },
      },
    };
    const old = { ...catchItem(), pond: null };
    await renderWithProviders(
      <>
        <FishingGiftSource outcome={gift} />
        <FishingPondReturn batchID={batchID} outcome={gift} isPublic onReceipt={vi.fn()} />
        <FishingPondReturn batchID={batchID} outcome={old} isPublic onReceipt={vi.fn()} />
      </>,
      options,
    );
    expect(screen.getByText('A gift from')).toBeVisible();
    expect(screen.getByText('Long donor '.repeat(10).trim())).toBeVisible();
    expect(document.querySelector('.fishing-gift-source img')).toHaveAttribute(
      'src',
      gift.pond.gift.avatarURL,
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('uses the release receipt for fry', async () => {
    installJsonFetchFixtures([
      { method: 'POST', path, body: { ...receipt, return_state: 'released' } },
    ]);
    const onReceipt = vi.fn();
    await renderWithProviders(
      <FishingPondReturn
        batchID={batchID}
        outcome={catchItem('fry')}
        isPublic
        onReceipt={onReceipt}
      />,
      options,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Release' }));
    await screen.findByText('Released');
    expect(onReceipt).toHaveBeenCalledWith(expect.objectContaining({ returnState: 'released' }));
  });
});

describe('Pond boards', () => {
  it('defaults to seven days, includes my outside rank, and can change period and retry errors', async () => {
    const row = { rank: '1', item_count: '12000', identity: { kind: 'anonymous' }, is_me: false };
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/games/fishing/pond/leaderboard?board=cleanup&period=7d',
        body: {
          board: 'cleanup',
          period: '7d',
          window_start: 1_700_000_000,
          entries: [row],
          me: { ...row, rank: '21', item_count: '2', is_me: true },
        },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/pond/leaderboard?board=cleanup&period=30d',
        status: 500,
        body: { error: { code: 'internal_error', message: 'Unavailable' } },
      },
      {
        method: 'GET',
        path: '/api/games/fishing/pond/leaderboard?board=cleanup&period=all',
        body: {
          board: 'cleanup',
          period: 'all',
          window_start: null,
          entries: [],
          me: null,
        },
      },
    ]);
    const rendered = await renderWithProviders(<FishingPondBoard board="cleanup" />, options);
    expect(screen.getByRole('radio', { name: 'Past 7 days' })).toBeChecked();
    await screen.findByText('Anonymous angler · You');
    expect(screen.getByText('12K')).toHaveAttribute('title', '12,000');
    fireEvent.click(screen.getByRole('radio', { name: 'Past 30 days' }));
    await screen.findByRole('alert');
    const before = fetchMock.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(before));
    fireEvent.click(screen.getByRole('radio', { name: 'All time' }));
    await screen.findByText('No records yet');
    expect(screen.getByRole('radio', { name: 'All time' })).toBeChecked();
    rendered.unmount();
  });
});
