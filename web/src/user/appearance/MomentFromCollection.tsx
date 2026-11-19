import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useArtworkPolicy } from '@shared/artwork/context';
import { ErrorState } from '@shared/components/States';
import { Button } from '@shared/components/ui/Button';
import { useOptionalAppearance } from './context';
import { momentByKey, momentArtworkKeys } from './moments';

export function MomentFromCollection({ artworkKey }: { artworkKey: string }) {
  const { t } = useTranslation();
  const appearance = useOptionalAppearance();
  const policy = useArtworkPolicy();
  const [error, setError] = useState<unknown>(null);
  const artwork = momentByKey.get(artworkKey);
  if (
    !appearance ||
    !artwork ||
    !appearance.state.unlocked.includes('heart-moment') ||
    !appearance.state.moments?.includes(artworkKey) ||
    !policy.allowed(momentArtworkKeys(artwork))
  )
    return null;

  const selected =
    appearance.state.selected === 'heart-moment' && appearance.state.moment === artworkKey;
  return (
    <>
      {error != null && <ErrorState error={error} />}
      <Button
        disabled={selected || appearance.saving}
        onClick={() => {
          setError(null);
          void appearance.select('heart-moment', artworkKey).catch(setError);
        }}
      >
        {t(selected ? 'user.appearance.moments.applied' : 'user.appearance.moments.fromCollection')}
      </Button>
    </>
  );
}
