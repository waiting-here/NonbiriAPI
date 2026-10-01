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
  const text = useDuelText();
  const count = events.filter((event) => event.cast?.success).length;
  const gain = to - from;
  const reached = from < target && to >= target;
  const surge = gain >= target / 2;
  return (
    <div className="likes-impact-slot">
      {count === 0 && overloaded && (
        <div className="likes-overload-signal">
          <span aria-hidden="true">ϟ</span>
          <strong>{text('likes.oVERLOAD')}</strong>
          <small>{text('likes.cASTINTERRUPTED')}</small>
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
                ? text('likes.tARGETREACHED')
                : surge
                  ? text('likes.lIKESSURGE')
                  : text('likes.sKILLCAST')}
            </strong>
            <span>
              {followUpCount > 0
                ? `${text('likes.fOLLOWUP')} ×${followUpCount}`
                : count > 1
                  ? `×${count} ${text('likes.cASTS')}`
                  : text('likes.lIKESAWARDED')}
            </span>
          </div>
          <strong className="likes-hit-value" aria-label={`${text('likes.likesChange')}: ${gain}`}>
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
