import { useDuelText } from '../duel/copy';
export function ArcadeAudioControls({
  sound,
  music,
  unavailable,
}: {
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
      <button
        type="button"
        className="btn btn-secondary"
        aria-pressed={sound.enabled}
        onClick={sound.toggle}
      >
        {sound.enabled ? text('common.soundOn') : text('common.soundOff')}
      </button>
      {music && (
        <button
          type="button"
          className="btn btn-secondary"
          aria-pressed={music.enabled}
          onClick={music.toggle}
        >
          {music.enabled ? text('common.musicOn') : text('common.musicOff')}
        </button>
      )}
      {unavailable && <small role="status">{text('common.someAudioCouldNotLoadSwitchIt')}</small>}
    </>
  );
}
