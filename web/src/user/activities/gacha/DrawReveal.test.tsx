import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { DrawReveal } from './DrawReveal';
import type { DrawReceipt, Preferences } from './types';
import type { GachaAudio } from './audio';
vi.mock('./Artwork', () => ({ Artwork: () => <span data-testid="artwork" /> }));

const receipt: DrawReceipt = {
  id: 'draw',
  pool_id: 'pool',
  pool_name: 'Stars',
  created_at: 1800000000,
  items: [
    { card_id: 'a-chatgpt', duplicate: false, guarantee: 'none' },
    { card_id: 'l-chatgpt', duplicate: true, guarantee: 'ssr' },
  ],
};
const preferences: Preferences = { music: false, sound: true, vibration: true, reduced: false };
const cue = vi.fn(),
  stopEffects = vi.fn(),
  vibrate = vi.fn();
const audio = { cue, stopEffects } as unknown as GachaAudio;
const advance = (ms: number) => act(() => vi.advanceTimersByTime(ms));
beforeEach(() => {
  vi.useFakeTimers();
  cue.mockClear();
  stopEffects.mockClear();
  vibrate.mockClear();
  Object.defineProperty(navigator, 'vibrate', { configurable: true, value: vibrate });
  Object.defineProperty(document, 'hidden', { configurable: true, value: false });
});
afterEach(() => {
  vi.useRealTimers();
});

it('automatically reveals in order, pauses remaining time and ends with every result', () => {
  const close = vi.fn();
  render(
    <DrawReveal
      receipt={receipt}
      preferences={preferences}
      audio={audio}
      english
      onClose={close}
    />,
  );
  advance(2550);
  expect(document.querySelector('[data-card-id="a-chatgpt"]')).toBeInTheDocument();
  advance(400);
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  advance(5000);
  expect(document.querySelector('[data-card-id="a-chatgpt"]')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
  advance(600);
  expect(document.querySelector('[data-card-id="l-chatgpt"]')).toBeInTheDocument();
  expect(vibrate).toHaveBeenCalledWith([65, 75, 110, 100, 160]);
  advance(5400);
  expect(screen.getByRole('region', { name: 'All draw results' })).toBeVisible();
  expect(screen.getByLabelText('Converted to 1 SSR shard')).toBeVisible();
  expect(document.querySelectorAll('.gacha-cinema__result')).toHaveLength(2);
  expect(close).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Keep these memories' }));
  expect(close).toHaveBeenCalledTimes(1);
});

it('skip immediately shows the complete ten-pull receipt and cancels scheduled scenes', () => {
  const ten = { ...receipt, items: Array.from({ length: 10 }, (_, i) => receipt.items[i % 2]) };
  render(
    <DrawReveal receipt={ten} preferences={preferences} audio={audio} english onClose={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Skip · view all' }));
  expect(document.querySelectorAll('.gacha-cinema__result')).toHaveLength(10);
  const cues = cue.mock.calls.length;
  advance(60000);
  expect(cue).toHaveBeenCalledTimes(cues);
  expect(document.querySelector('[data-card-id]')).toBeNull();
  expect(vibrate).toHaveBeenCalledWith(0);
  expect(stopEffects).toHaveBeenCalled();
});

it('hidden tabs preserve progress and reduced motion goes directly to results without haptics', () => {
  const rendered = render(
    <DrawReveal
      receipt={receipt}
      preferences={preferences}
      audio={audio}
      english
      onClose={vi.fn()}
    />,
  );
  advance(2550);
  advance(300);
  act(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  advance(10000);
  expect(document.querySelector('[data-card-id="a-chatgpt"]')).toBeInTheDocument();
  act(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  advance(700);
  expect(document.querySelector('[data-card-id="l-chatgpt"]')).toBeInTheDocument();
  rendered.unmount();
  vibrate.mockClear();
  cue.mockClear();
  render(
    <DrawReveal
      receipt={receipt}
      preferences={{ ...preferences, reduced: true }}
      audio={audio}
      english
      onClose={vi.fn()}
    />,
  );
  expect(screen.getByRole('region', { name: 'All draw results' })).toBeVisible();
  expect(vibrate).not.toHaveBeenCalled();
  expect(cue).not.toHaveBeenCalled();
});
