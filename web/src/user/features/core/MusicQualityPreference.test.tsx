import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { MusicQualityPreference } from './MusicQualityPreference';
import { MUSIC_QUALITY_KEY } from '../../games/common/audio/preferences';

vi.mock('./copy', () => ({ useCoreCopy: () => ({ t: (key: string) => key }) }));
beforeEach(() => localStorage.clear());
it('defaults to lightweight and stores only the local browser preference', () => {
  const fetcher = vi.spyOn(globalThis, 'fetch');
  render(<MusicQualityPreference />);
  expect(screen.getByRole('radio', { name: 'account.musicLight' })).toBeChecked();
  fireEvent.click(screen.getByRole('radio', { name: 'account.musicLossless' }));
  expect(localStorage.getItem(MUSIC_QUALITY_KEY)).toBe('lossless');
  expect(screen.getByRole('radio', { name: 'account.musicLossless' })).toBeChecked();
  expect(fetcher).not.toHaveBeenCalled();
});
