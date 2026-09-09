import { useGameCopy } from '../copy';
import type { GameSoundControl } from './useGameSound';

export function GameSoundButton({ sound }: { readonly sound: GameSoundControl }) {
  const { text } = useGameCopy();
  return (
    <button
      type="button"
      className="btn btn-secondary"
      aria-pressed={sound.enabled}
      onClick={sound.toggle}
    >
      {text(sound.enabled ? 'fishing.sound.on' : 'fishing.sound.off')}
    </button>
  );
}
