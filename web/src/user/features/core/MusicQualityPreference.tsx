import { useState } from 'react';
import { readMusicQuality, writeMusicQuality } from '../../games/common/audio/preferences';
import type { MusicQuality } from '../../games/common/audio/assets';
import { useCoreCopy } from './copy';

export function MusicQualityPreference() {
  const { t } = useCoreCopy();
  const [quality, setQuality] = useState(readMusicQuality);
  return (
    <fieldset>
      <legend>{t('account.musicQuality')}</legend>
      <div className="core-radio-group">
        {(['light', 'lossless'] as const).map((value: MusicQuality) => (
          <label key={value}>
            <input
              type="radio"
              name="account-music-quality"
              checked={quality === value}
              onChange={() => {
                setQuality(value);
                writeMusicQuality(value);
              }}
            />
            <span>{t(value === 'light' ? 'account.musicLight' : 'account.musicLossless')}</span>
          </label>
        ))}
      </div>
      <p className="core-muted">{t('account.musicQualityBody')}</p>
    </fieldset>
  );
}
