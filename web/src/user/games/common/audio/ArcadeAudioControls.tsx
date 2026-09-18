import { useDuelText } from '../duel/copy';

export function ArcadeAudioControls({
  sound,
  music,
  unavailable,
}: {
  readonly sound: { enabled: boolean; toggle: () => void };
  readonly music?: { enabled: boolean; toggle: () => void };
  readonly unavailable?: boolean;
}) {
  const t = useDuelText();
  return (
    <>
      <button
        type="button"
        className="btn btn-secondary"
        aria-pressed={sound.enabled}
        onClick={sound.toggle}
      >
        {sound.enabled ? t('音效：开', 'Sound: on') : t('音效：关', 'Sound: off')}
      </button>
      {music && (
        <button
          type="button"
          className="btn btn-secondary"
          aria-pressed={music.enabled}
          onClick={music.toggle}
        >
          {music.enabled ? t('音乐：开', 'Music: on') : t('音乐：关', 'Music: off')}
        </button>
      )}
      {unavailable && (
        <small role="status">
          {t(
            '部分声音未能加载，可关闭后重试。',
            'Some audio could not load. Switch it off and on to retry.',
          )}
        </small>
      )}
    </>
  );
}
