import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useArtworkPolicy } from '@shared/artwork/context';
import type { AttendanceCover } from './api';
import { defaultCover, resolveCover } from './coverChoices';
import './presentation.css';

interface CoverProps {
  cover?: AttendanceCover;
  className?: string;
  onLoadError?: (failed: boolean) => void;
}

export function CampaignCover(props: CoverProps) {
  return <CoverImage key={JSON.stringify([props.cover?.kind, props.cover?.value])} {...props} />;
}

function CoverImage({ cover, className = '', onLoadError }: CoverProps) {
  const { t } = useTranslation();
  const policy = useArtworkPolicy();
  const [failed, setFailed] = useState(false);
  const [revealed, setRevealed] = useState(false);
  const image = resolveCover(cover);
  const key = image?.artworkKey;
  const allowed = image && (!key || (policy.allowed(key) && policy.sourceAllowed(image.src)));
  const gated = allowed && key && policy.rating(key) === 'r18';
  const custom = allowed && (!gated || revealed) && !failed;
  const external = custom && cover?.kind === 'external';
  return (
    <div className={`attendance-cover ${className}`} data-fit={custom ? cover?.fit : 'default'}>
      <img
        src={custom ? image.src : defaultCover}
        alt=""
        width={1672}
        height={941}
        loading="lazy"
        referrerPolicy={external ? 'no-referrer' : undefined}
        crossOrigin={external ? 'anonymous' : undefined}
        onLoad={() => {
          if (custom) onLoadError?.(false);
        }}
        onError={() => {
          if (custom) {
            setFailed(true);
            onLoadError?.(true);
          }
        }}
      />
      {gated && !failed && (
        <button
          type="button"
          className="attendance-cover__reveal nb-btn nb-btn--secondary"
          aria-pressed={revealed}
          onClick={() => setRevealed(!revealed)}
        >
          {t(revealed ? 'attendanceDisplay.hideRated' : 'attendanceDisplay.showRated')}
        </button>
      )}
    </div>
  );
}
