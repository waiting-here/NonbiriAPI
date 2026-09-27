import { useState } from 'react';
import type { PublicIdentity } from './strict';
import './publicGameIdentity.css';

function discordAvatarURL(value: string | null): string | null {
  if (!value) return null;
  try {
    const parsed = new URL(value);
    if (
      parsed.protocol !== 'https:' ||
      parsed.username ||
      parsed.password ||
      parsed.port ||
      !['cdn.discordapp.com', 'media.discordapp.net'].includes(parsed.hostname)
    )
      return null;
    return parsed.href;
  } catch {
    return null;
  }
}

export function PublicGameIdentity({
  identity,
  anonymousLabel,
  isMe = false,
  meLabel = '',
  size = 28,
}: {
  readonly identity: PublicIdentity;
  readonly anonymousLabel: string;
  readonly isMe?: boolean;
  readonly meLabel?: string;
  readonly size?: number;
}) {
  const [failedURL, setFailedURL] = useState<string | null>(null);
  const avatar = identity.kind === 'public' ? discordAvatarURL(identity.avatarURL) : null;
  const name = identity.kind === 'public' ? identity.displayName : anonymousLabel;
  const initial = identity.kind === 'public' ? (Array.from(name.trim())[0] ?? '?') : '?';
  return (
    <span className="public-game-identity">
      {avatar && failedURL !== avatar ? (
        <img
          src={avatar}
          alt=""
          width={size}
          height={size}
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setFailedURL(avatar)}
        />
      ) : (
        <span
          className="public-game-identity__placeholder"
          style={{ width: size, height: size }}
          aria-hidden="true"
        >
          {initial}
        </span>
      )}
      <span className="public-game-identity__name">
        {name}
        {isMe && meLabel ? ` · ${meLabel}` : ''}
      </span>
    </span>
  );
}
