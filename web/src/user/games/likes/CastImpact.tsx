import { useDuelText } from '../common/duel/copy';
import { interpolate } from './motion';
import type { LikesEvent } from './types';

export function CastImpact({
  events,
  from,
  to,
  target,
  progress,
  reduced,
  overloaded = false,
  followUpCount = 0,
}: {
  readonly events: readonly LikesEvent[];
  readonly from: number;
  readonly to: number;
  readonly target: number;
  readonly progress: number;
  readonly reduced: boolean;
  readonly overloaded?: boolean;
  readonly followUpCount?: number;
}) {
  const t = useDuelText();
  const count = events.filter((event) => event.cast?.success).length;
  const gain = to - from;
  const reached = from < target && to >= target;
  const surge = gain >= target / 2;
  return (
    <div className="likes-impact-slot">
      {count === 0 && overloaded && (
        <div className="likes-overload-signal">
          <span aria-hidden="true">ϟ</span>
          <strong>{t('过载', 'OVERLOAD')}</strong>
          <small>{t('释放受阻', 'CAST INTERRUPTED')}</small>
        </div>
      )}
      {count > 0 && (
        <div
          className={`likes-hit ${surge ? 'likes-hit--surge' : ''} ${reached ? 'likes-hit--target' : ''}`}
          data-awarded={gain}
        >
          <div className="likes-hit-rays" aria-hidden="true">
            <i />
            <i />
            <i />
            <i />
            <i />
            <i />
          </div>
          <div className="likes-hit-label">
            <strong>
              {reached
                ? t('目标达成', 'TARGET REACHED')
                : surge
                  ? t('得赞爆发', 'LIKES SURGE')
                  : t('技能释放', 'SKILL CAST')}
            </strong>
            <span>
              {followUpCount > 0
                ? `${t('连答', 'FOLLOW-UP')} ×${followUpCount}`
                : count > 1
                  ? `×${count} ${t('施放', 'CASTS')}`
                  : t('实得赞', 'LIKES AWARDED')}
            </span>
          </div>
          <strong
            className="likes-hit-value"
            aria-label={`${t('得赞变化', 'Likes change')}: ${gain}`}
          >
            <span aria-hidden="true">
              {gain >= 0 ? '+' : ''}
              {reduced ? gain : interpolate(0, gain, progress)}
            </span>
            <small aria-hidden="true">♥</small>
          </strong>
        </div>
      )}
    </div>
  );
}
