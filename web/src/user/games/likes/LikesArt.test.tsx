import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import mark from '@shared/assets/nonbiri-mark.svg';
import { artRegistry } from './art';
import { LikesArt } from './LikesArt';

describe('LikesArt image integration', () => {
  it('uses an independent transparent WebP derivative for every approved slot', () => {
    const slots = Object.values(artRegistry);
    expect(slots).toHaveLength(127);
    expect(new Set(slots.map((slot) => slot.source)).size).toBe(127);
    expect(slots.every((slot) => !slot.placeholder)).toBe(true);
    expect(slots.every((slot) => slot.source.endsWith('.webp'))).toBe(true);
    expect(slots.every((slot) => slot.sourceFile.includes('/game-likes/'))).toBe(true);
  });

  it('falls back to the local SVG mark after an image load failure', () => {
    const slot = artRegistry['role.ChatGPT.portrait'];
    const { container } = render(<LikesArt slot={slot} label="ChatGPT" />);
    const image = screen.getByRole('img', { name: 'ChatGPT' });
    fireEvent.error(image);
    expect(container.querySelector('figure')).toHaveAttribute('data-fallback', 'true');
    expect(image).toHaveAttribute('src', mark);
  });
});
