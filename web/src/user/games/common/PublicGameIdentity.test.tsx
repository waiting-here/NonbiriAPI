import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PublicGameIdentity } from './PublicGameIdentity';

describe('PublicGameIdentity', () => {
  it('uses only the server public projection and a Discord CDN avatar', () => {
    const { container } = render(
      <PublicGameIdentity
        identity={{
          kind: 'public',
          displayName: 'A very long Discord nickname with spaces',
          avatarURL: 'https://cdn.discordapp.com/avatars/123/avatar.png?size=64',
        }}
        anonymousLabel="Anonymous player"
        isMe
        meLabel="Me"
      />,
    );
    expect(screen.getByText('A very long Discord nickname with spaces · Me')).toBeVisible();
    const image = container.querySelector('img');
    expect(image).not.toBeNull();
    if (!image) throw new Error('public avatar is missing');
    expect(image).toHaveAttribute('referrerpolicy', 'no-referrer');
    expect(image).toHaveAttribute('width', '28');
    fireEvent.error(image);
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('A very long Discord nickname with spaces · Me')).toBeVisible();
  });

  it('never fetches an anonymous or non-Discord avatar', () => {
    const { container, rerender } = render(
      <PublicGameIdentity identity={{ kind: 'anonymous' }} anonymousLabel="Anonymous tycoon" />,
    );
    expect(screen.getByText('Anonymous tycoon')).toBeVisible();
    expect(container.querySelector('img')).toBeNull();
    rerender(
      <PublicGameIdentity
        identity={{
          kind: 'public',
          displayName: 'Player',
          avatarURL: 'https://cdn.discordapp.com.evil.invalid/avatars/123/avatar.png',
        }}
        anonymousLabel="Anonymous tycoon"
      />,
    );
    expect(screen.getByText('Player')).toBeVisible();
    expect(container.querySelector('img')).toBeNull();
  });
});
