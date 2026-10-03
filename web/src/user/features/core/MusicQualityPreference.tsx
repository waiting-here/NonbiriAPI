import { Segmented } from '@shared/components/ui';
import { useState } from 'react';
import { readMusicQuality, writeMusicQuality } from '../../games/common/audio/preferences';
import type { MusicQuality } from '../../games/common/audio/assets';
import { useCoreCopy } from './copy';

export function MusicQualityPreference() {
  const { t } = useCoreCopy();
  const [quality, setQuality] = useState(readMusicQuality);
  return (
    <div className="account-preference-row">
      <div>
        <h3>{t('account.musicQuality')}</h3>
        <p className="core-muted">{t('account.musicQualityBody')}</p>
      </div>
      <Segmented
        label={t('account.musicQuality')}
        value={quality}
        options={(['light', 'lossless'] as const).map((value) => ({
          value,
          label: t(value === 'light' ? 'account.musicLight' : 'account.musicLossless'),
        }))}
        onChange={(value: MusicQuality) => {
          setQuality(value);
          writeMusicQuality(value);
        }}
      />
    </div>
  );
}
