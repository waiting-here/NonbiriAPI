import { GameHeaderTool } from '../GameHeader';
import { useDuelText } from '../duel/copy';
export function ArcadeAudioControls({
  sound,
  music,
  unavailable,
  compact = false,
}: {
  readonly compact?: boolean;
  readonly sound: {
    enabled: boolean;
    toggle: () => void;
  };
  readonly music?: {
    enabled: boolean;
    toggle: () => void;
  };
  readonly unavailable?: boolean;
}) {
  const text = useDuelText();
  return (
    <>
      {compact ? (
        <GameHeaderTool
          icon="♪"
          label={sound.enabled ? text('common.soundOn') : text('common.soundOff')}
          aria-pressed={sound.enabled}
          onClick={sound.toggle}
        />
      ) : (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          aria-pressed={sound.enabled}
          onClick={sound.toggle}
        >
          {sound.enabled ? text('common.soundOn') : text('common.soundOff')}
        </button>
      )}
      {music &&
        (compact ? (
          <GameHeaderTool
            icon="♫"
            label={music.enabled ? text('common.musicOn') : text('common.musicOff')}
            aria-pressed={music.enabled}
            onClick={music.toggle}
          />
        ) : (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            aria-pressed={music.enabled}
            onClick={music.toggle}
          >
            {music.enabled ? text('common.musicOn') : text('common.musicOff')}
          </button>
        ))}
      {unavailable && <small role="status">{text('common.someAudioCouldNotLoadSwitchIt')}</small>}
    </>
  );
}
