import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '../../user/i18n';
import { afterEach, expect, it, vi } from 'vitest';
import source from '../../../../internal/artwork/catalog.json';
import { createArtworkCatalog, type ArtworkCatalog } from './catalog';
import { ArtworkPolicyProvider } from './policy';
import { ArtworkImage } from './ArtworkImage';
import { Artwork } from '../../user/activities/gacha/Artwork';
import { CardTile } from '../../user/activities/gacha/CardTile';
import { DrawReveal } from '../../user/activities/gacha/DrawReveal';
import { cardById } from '../../user/activities/gacha/catalog';
import type { GachaAudio } from '../../user/activities/gacha/audio';
import { renderSSR } from '../../user/activities/gacha/ssr-renderer';
import { AppearanceProvider } from '../../user/appearance/AppearanceProvider';
import {
  AppearanceContext,
  useOptionalAppearance,
  type AppearancePort,
} from '../../user/appearance/context';
import { MomentBackdrop } from '../../user/appearance/MomentBackdrop';
import { MomentPicker } from '../../user/appearance/MomentPicker';

vi.mock('../../user/activities/gacha/ssr-renderer', () => ({ renderSSR: vi.fn() }));
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function catalog(...keys: string[]) {
  const fixture = structuredClone(source) as ArtworkCatalog;
  for (const key of keys) fixture.artworks[key].rating = 'r18';
  return createArtworkCatalog(fixture);
}
it('labels a rated candidate and stops its renderer when disabled, while allowed detail has no badge', async () => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const registry = catalog('card:g-chatgpt');
  const stop = vi.fn();
  vi.mocked(renderSSR).mockResolvedValue(stop);
  const card = cardById['g-chatgpt'];
  const tile = render(
    <ArtworkPolicyProvider disabled catalog={registry}>
      <CardTile card={card} onClick={vi.fn()} />
    </ArtworkPolicyProvider>,
  );
  expect(screen.getByText('R-18')).toBeVisible();
  expect(tile.container.querySelector('img')).toBeNull();
  expect(tile.container.querySelector('canvas')).toBeNull();
  tile.unmount();
  const tree = (disabled: boolean) => (
    <ArtworkPolicyProvider disabled={disabled} catalog={registry}>
      <Artwork card={card} full animated />
    </ArtworkPolicyProvider>
  );
  const view = render(tree(false));
  await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
  expect(screen.queryByText('R-18')).toBeNull();
  const signal = vi.mocked(renderSSR).mock.calls[0][5]?.signal;
  view.rerender(tree(true));
  await waitFor(() => expect(stop).toHaveBeenCalledOnce());
  expect(signal?.aborted).toBe(true);
  expect(view.container.querySelector('img')).toBeNull();
  expect(view.container.querySelector('canvas')).toBeNull();
  expect(screen.getByRole('img')).toHaveAccessibleName(card.name_en);
});

it('keeps saved appearance state raw while the effective background falls back and restores', async () => {
  vi.stubGlobal('matchMedia', () => ({
    matches: true,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
  const registry = catalog('card:l-chatgpt');
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const select = vi.fn();
  const port: AppearancePort = {
    scope: 'rated-appearance',
    load: async () => ({
      selected: 'celestial',
      unlocked: ['default', 'celestial'],
      updated_at: 1,
    }),
    select,
  };
  function RawState() {
    return <span data-testid="saved">{useOptionalAppearance()?.state.selected}</span>;
  }
  const tree = (disabled: boolean) => (
    <QueryClientProvider client={client}>
      <ArtworkPolicyProvider disabled={disabled} catalog={registry}>
        <AppearanceProvider port={port}>
          <RawState />
        </AppearanceProvider>
      </ArtworkPolicyProvider>
    </QueryClientProvider>
  );
  const view = render(tree(true));
  await screen.findByText('celestial');
  expect(document.documentElement.dataset.appearance).toBe('default');
  expect(view.container.querySelector('img')).toBeNull();
  view.rerender(tree(false));
  await waitFor(() => expect(document.documentElement.dataset.appearance).toBe('celestial'));
  expect(view.container.querySelector('img')?.getAttribute('src')).toContain('celestial');
  expect(screen.queryByText('R-18')).toBeNull();
  expect(select).not.toHaveBeenCalled();
  view.unmount();
  client.clear();
});

it('gates a card moment by its scenery and keeps the unavailable current preview from loading', () => {
  const registry = catalog('illustration:a');
  const preferred = 'card:a-chatgpt';
  const state = {
    selected: 'heart-moment',
    moment: preferred,
    moments: ['cover:permanent', preferred],
    unlocked: ['default', 'heart-moment'],
    updated_at: 1,
  };
  const select = vi.fn();
  const tree = (disabled: boolean) => (
    <ArtworkPolicyProvider disabled={disabled} catalog={registry}>
      <AppearanceContext
        value={{ state, pending: false, saving: false, error: null, refresh: vi.fn(), select }}
      >
        <MomentPicker />
        <MomentBackdrop moment={preferred} />
      </AppearanceContext>
    </ArtworkPolicyProvider>
  );
  const view = render(tree(true));
  const option = view.container.querySelector('input[value="' + preferred + '"]');
  expect(option).toBeDisabled();
  expect(view.container.querySelector('.nb-moment-picker__preview')).not.toHaveAttribute('src');
  expect(view.container.querySelector('.nb-moment-backdrop__scene')).toHaveAttribute(
    'src',
    '/assets/gacha/covers/permanent.webp',
  );
  expect(
    view.container.querySelectorAll('img[src*="a-chatgpt"], img[src*="backgrounds/a"]'),
  ).toHaveLength(0);
  expect(screen.getAllByText('R-18').length).toBeGreaterThan(0);
  expect(screen.getByText(/^1 available\./)).toBeVisible();
  fireEvent.click(option!);
  expect(select).not.toHaveBeenCalled();
  view.rerender(tree(false));
  expect(view.container.querySelector('input[value="' + preferred + '"]')).toBeEnabled();
  expect(screen.getByText(/^2 available\./)).toBeVisible();
  expect(view.container.querySelector('.nb-moment-backdrop__scene')).toHaveAttribute(
    'src',
    '/assets/gacha/backgrounds/a.webp',
  );
  expect(state.moment).toBe(preferred);
});

it('does not preload rated draw art or fetch rated closeup scenery', async () => {
  const registry = catalog('card:g-chatgpt', 'illustration:g');
  const requests: string[] = [];
  vi.stubGlobal(
    'Image',
    class {
      set src(value: string) {
        requests.push(value);
      }
    },
  );
  const audio = { cue: vi.fn(), stopEffects: vi.fn() } as unknown as GachaAudio;
  const receipt = {
    id: 'safe-receipt',
    pool_id: 'permanent',
    pool_name: 'Test',
    created_at: 1,
    items: [{ card_id: 'g-chatgpt', duplicate: false, guarantee: 'none' as const }],
  };
  const view = render(
    <ArtworkPolicyProvider disabled catalog={registry}>
      <DrawReveal
        receipt={receipt}
        preferences={{ music: false, sound: false, reduced: true }}
        audio={audio}
        english
        onClose={vi.fn()}
      />
    </ArtworkPolicyProvider>,
  );
  await act(async () => {});
  expect(requests).toEqual([]);
  expect(renderSSR).not.toHaveBeenCalled();
  expect(
    view.container.querySelectorAll('img[src*="g-chatgpt"], img[src*="/groups/g.webp"]'),
  ).toHaveLength(0);
  expect(screen.queryByText('R-18')).toBeNull();
  expect(receipt.items).toEqual([{ card_id: 'g-chatgpt', duplicate: false, guarantee: 'none' }]);
  view.unmount();
});

it('filters mixed responsive sources and retains only the permanent fallback while disabled', () => {
  const registry = catalog('card:g-chatgpt');
  const src = cardById['a-chatgpt'].art;
  const srcSet = src + ' 1024w, /assets/gacha/hires/cards/g-chatgpt.webp 2560w';
  const tree = (disabled: boolean, fallbackSrc?: string) => (
    <ArtworkPolicyProvider disabled={disabled} catalog={registry}>
      <ArtworkImage
        artworkKey="card:a-chatgpt"
        src={src}
        srcSet={srcSet}
        alt="Safe card"
        fallbackSrc={fallbackSrc}
      />
    </ArtworkPolicyProvider>
  );
  const view = render(tree(true));
  expect(view.container.querySelector('img')).toBeNull();
  view.rerender(tree(true, '/assets/gacha/covers/permanent.webp'));
  expect(view.container.querySelector('img')).toHaveAttribute(
    'src',
    '/assets/gacha/covers/permanent.webp',
  );
  expect(view.container.querySelector('img')).not.toHaveAttribute('srcset');
  view.rerender(tree(false));
  expect(view.container.querySelector('img')).toHaveAttribute('src', src);
  expect(view.container.querySelector('img')).toHaveAttribute('srcset', srcSet);
});
