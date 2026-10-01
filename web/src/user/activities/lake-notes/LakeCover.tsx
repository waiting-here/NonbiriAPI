import { useState } from 'react';
import { useLakeCopy } from './copy';
import './cover.css';

export function LakeCover() {
  const { t: text } = useLakeCopy(),
    [failed, setFailed] = useState(false);
  return failed ? (
    <div className="lake-cover-fallback" role="img" aria-label={text('coverAlt')}>
      {text('title')}
    </div>
  ) : (
    <img
      className="lake-cover"
      src="/assets/lake-notes/cover.png"
      alt={text('coverAlt')}
      width="1672"
      height="941"
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}
