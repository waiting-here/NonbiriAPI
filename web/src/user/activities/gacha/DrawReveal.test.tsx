import { StrictMode } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { DrawReveal } from './DrawReveal';
import { cardById } from './catalog';
import type { DrawReceipt, Preferences } from './types';
import type { GachaAudio } from './audio';
vi.mock('./Artwork', () => ({ Artwork: () => <span data-testid="artwork" /> }));

const item = (card_id: string, duplicate = false) => ({
  card_id,
  duplicate,
  guarantee: 'none' as const,
});
const receipt = (items = [item('a-chatgpt'), item('l-chatgpt', true)]): DrawReceipt => ({
  id: 'draw',
  pool_id: 'pool',
  pool_name: 'Stars',
  created_at: 1800000000,
  items,
});
const preferences: Preferences = { music: false, sound: true, vibration: true, reduced: false };
const cue = vi.fn(),
  stopEffects = vi.fn(),
  vibrate = vi.fn();
const audio = { cue, stopEffects } as unknown as GachaAudio;
const advance = (ms: number) => {
  act(() => vi.advanceTimersByTime(ms));
  act(() => vi.advanceTimersByTime(0));
};
const phase = () => document.querySelector('.gacha-draw-stage')?.getAttribute('data-phase');
const openIDs = () =>
  [...document.querySelectorAll('.gacha-draw-slot[data-open="true"]')].map((node) =>
    node.getAttribute('data-card-id'),
  );
function show(draw = receipt(), prefs = preferences, onAgain?: () => void) {
  const close = vi.fn();
  return {
    close,
    ...render(
      <DrawReveal
        receipt={draw}
        preferences={prefs}
        audio={audio}
        english
        onClose={close}
        onAgain={onAgain}
      />,
    ),
  };
}
function deal(quantity: number) {
  advance(2200);
  expect(phase()).toBe('deal');
  advance(quantity === 1 ? 620 : quantity * 70 + 620);
}
function background(hidden: boolean) {
  act(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: hidden });
    document.dispatchEvent(new Event('visibilitychange'));
  });
}
beforeEach(() => {
  vi.useFakeTimers();
  cue.mockClear();
  stopEffects.mockClear();
  vibrate.mockClear();
  Object.defineProperty(navigator, 'vibrate', { configurable: true, value: vibrate });
  Object.defineProperty(document, 'hidden', { configurable: true, value: false });
});
afterEach(() => vi.useRealTimers());

it('reveals receipt order, preserves paused time, and settles duplicate shards once', () => {
  const { close } = show();
  deal(2);
  expect(openIDs()).toEqual(['a-chatgpt']);
  advance(130);
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  advance(5000);
  expect(openIDs()).toEqual(['a-chatgpt']);
  fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
  advance(199);
  expect(phase()).toBe('flip');
  advance(1);
  expect(phase()).toBe('charge');
  advance(900);
  expect(phase()).toBe('closeup');
  expect(vibrate).toHaveBeenCalledWith([65, 75, 110, 100, 160]);
  advance(60000);
  expect(phase()).toBe('closeup');
  fireEvent.click(document.querySelector('.gacha-cinema__caption')!);
  expect(phase()).toBe('settle');
  expect(screen.getByLabelText('Converted to 1 SSR shard')).toBeVisible();
  advance(1200);
  expect(phase()).toBe('done');
  expect(openIDs()).toEqual(['a-chatgpt', 'l-chatgpt']);
  expect(cue.mock.calls.map((call) => call[0])).toEqual(['gather', 'N', 'SP', 'spread']);
  fireEvent.click(screen.getByRole('button', { name: 'Keep these memories' }));
  expect(close).toHaveBeenCalledTimes(1);
});

it('skip preserves all ten results without cue replay, draw-again, or automatic closing', () => {
  const ten = receipt(
    Array.from({ length: 10 }, (_, i) => item(i % 2 ? 'l-chatgpt' : 'a-chatgpt', i > 0)),
  );
  const { close } = show(ten);
  fireEvent.click(screen.getByRole('button', { name: 'Skip · view all' }));
  expect(phase()).toBe('done');
  expect(openIDs()).toEqual(ten.items.map((value) => value.card_id));
  const cues = cue.mock.calls.length;
  advance(60000);
  expect(cue).toHaveBeenCalledTimes(cues);
  expect(screen.queryByRole('button', { name: 'Draw again' })).not.toBeInTheDocument();
  expect(close).not.toHaveBeenCalled();
  expect(stopEffects).toHaveBeenCalled();
  expect(vibrate).toHaveBeenCalledWith(0);
  fireEvent.click(document.querySelector('.gacha-draw-slot')!);
  expect(screen.getByRole('dialog', { name: cardById['a-chatgpt'].name_en })).toBeVisible();
});

it('retains remaining scene time while the document is hidden', () => {
  show();
  advance(900);
  background(true);
  advance(10000);
  expect(phase()).toBe('gather');
  background(false);
  advance(1299);
  expect(phase()).toBe('gather');
  advance(1);
  expect(phase()).toBe('deal');
});

it.each(['e-chatgpt', 'g-chatgpt', 'l-chatgpt'])(
  'holds %s until clicked, with SR staying in the board',
  (cardID) => {
    show(receipt([item(cardID), item('a-chatgpt')]));
    deal(2);
    if (cardID !== 'e-chatgpt') advance(900);
    if (cardID === 'l-chatgpt') {
      expect(phase()).toBe('prelude');
      advance(1800);
    }
    advance(60000);
    expect(document.querySelector('.gacha-draw-sr-caption') !== null).toBe(cardID === 'e-chatgpt');
    expect(document.querySelector('.gacha-cinema__encounter') !== null).toBe(
      cardID !== 'e-chatgpt',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
    fireEvent.click(document.querySelector('.gacha-draw-stage')!);
    expect(openIDs()).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
    fireEvent.click(document.querySelector('.gacha-draw-stage')!);
    expect(openIDs()).toEqual([cardID, 'a-chatgpt']);
    advance(330);
    expect(phase()).toBe('settle');
  },
);

it.each([false, true])(
  'SP duplicate=%s chooses its prelude and voice without repeating the cue',
  (duplicate) => {
    show(receipt([item('l-chatgpt', duplicate)]));
    deal(1);
    advance(900);
    expect(phase()).toBe(duplicate ? 'closeup' : 'prelude');
    if (!duplicate) advance(1800);
    expect(document.querySelectorAll('.gacha-voice-after')).toHaveLength(duplicate ? 0 : 1);
    expect(cue.mock.calls.filter((call) => call[0] === 'SP')).toHaveLength(1);
  },
);

it('card-back hints are optional and N/R never expose their rarity early', () => {
  const draw = receipt(
    ['a-chatgpt', 'c-chatgpt', 'e-chatgpt', 'g-chatgpt', 'l-chatgpt'].map((id) => item(id)),
  );
  const view = show(draw);
  advance(1500);
  expect(document.querySelector('.gacha-cinema')).toHaveClass('gacha-cinema--SP');
  advance(700);
  expect(
    [...document.querySelectorAll('.gacha-draw-slot')].map((node) =>
      node.getAttribute('data-tell'),
    ),
  ).toEqual([null, null, 'SR', 'SSR', 'SP']);
  view.unmount();
  show(draw, { ...preferences, cardBackTell: false });
  advance(1500);
  expect(document.querySelector('.gacha-cinema')).toHaveClass('gacha-cinema--N');
  advance(700);
  expect(document.querySelectorAll('[data-tell]')).toHaveLength(0);
});

it('reduced motion opens the final receipt without cues or haptics and supports draw again', () => {
  const again = vi.fn();
  show(receipt(), { ...preferences, reduced: true }, again);
  expect(phase()).toBe('done');
  expect(cue).not.toHaveBeenCalled();
  expect(vibrate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Draw again' }));
  expect(again).toHaveBeenCalledTimes(1);
});

it('starts the gather cue once after strict lifecycle setup', () => {
  render(
    <StrictMode>
      <DrawReveal
        receipt={receipt()}
        preferences={preferences}
        audio={audio}
        english
        onClose={vi.fn()}
      />
    </StrictMode>,
  );
  advance(0);
  expect(cue.mock.calls.map((call) => call[0])).toEqual(['gather']);
});
