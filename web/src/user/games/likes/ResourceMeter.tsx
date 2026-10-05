import { interpolate } from './motion';
import { useDuelText } from '../common/duel/copy';
import type { ResourceShortage } from './shortage';
export function ResourceMeter({
  label,
  from,
  to,
  cap,
  progress = 1,
  reduced = false,
  tone = '',
  unit = '',
  shortage,
  shortagePulse = false,
  lockedCap = 0,
  lockedBalance = 0,
}: {
  readonly label: string;
  readonly from: number;
  readonly to: number;
  readonly cap?: number;
  readonly progress?: number;
  readonly reduced?: boolean;
  readonly tone?: string;
  readonly unit?: string;
  readonly shortage?: ResourceShortage;
  readonly shortagePulse?: boolean;
  readonly lockedCap?: number;
  readonly lockedBalance?: number;
}) {
  const text = useDuelText();
  const value = reduced ? to : interpolate(from, to, progress),
    extent = Math.max(cap ?? 0, from, to, 1),
    delta = to - from;
  return (
    <div
      className={`likes-meter ${shortage ? 'likes-meter--shortage' : ''} ${cap === undefined ? 'likes-counter' : ''} ${tone} ${delta > 0 ? 'is-gaining' : delta < 0 ? 'is-spending' : ''} ${delta !== 0 && cap !== undefined && Math.abs(delta) >= cap / 2 ? 'likes-meter--major' : ''}`}
      data-shortage={shortage?.resource}
      data-shortage-pulse={!!shortage && shortagePulse && !reduced}
      data-resource-label={label}
      data-from={from}
      data-to={to}
    >
      <div className="likes-meter-label">
        <span>{label}</span>
        <strong aria-label={`${label}: ${to}${unit}`}>
          <span aria-hidden="true">{value}</span>
          <small aria-hidden="true">
            {cap !== undefined ? ` / ${cap}` : ''}
            {unit}
          </small>
        </strong>
      </div>
      {cap !== undefined && (
        <div
          className="likes-meter-track"
          role="meter"
          aria-label={label}
          aria-valuenow={to}
          aria-valuemin={0}
          aria-valuemax={extent}
        >
          <span className="likes-meter-trail" style={{ width: `${(from / extent) * 100}%` }} />
          <span className="likes-meter-fill" style={{ width: `${(value / extent) * 100}%` }} />
          {lockedCap > 0 && (
            <>
              <span
                className="likes-meter-locked"
                style={{ width: `${(lockedCap / extent) * 100}%` }}
              />
              <span
                className="likes-meter-locked-balance"
                style={{
                  left: `${((extent - lockedCap) / extent) * 100}%`,
                  width: `${(lockedBalance / extent) * 100}%`,
                }}
              />
            </>
          )}
        </div>
      )}
      {lockedCap > 0 && (
        <small className="likes-lock-note">
          {text('likes.lockedBurst', {
            cap: lockedCap,
            balance: lockedBalance,
            available: Math.max(0, (cap ?? 0) - lockedCap),
          })}
        </small>
      )}
      {delta !== 0 && (
        <div className="likes-meter-change">
          <span>
            {from} → {to}
          </span>
          <strong className={reduced ? '' : 'likes-delta'}>
            {delta > 0 ? '+' : ''}
            {delta}
          </strong>
        </div>
      )}
      {shortage && (
        <small className="likes-resource-warning" role="status">
          {text('likes.atOverload')}: {text('likes.need')} {shortage.required}
          {unit} · {text('likes.available2')} {shortage.available}
          {unit}
        </small>
      )}
    </div>
  );
}
