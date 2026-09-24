import { useTranslation } from 'react-i18next';
import type { CharitySuccess } from '@shared/operations/charitySuccess';
import { charityControlCopy } from './charityControlCopy';

export function DonationThanks({ value }: { value?: boolean | null }) {
  const copy = charityControlCopy(useTranslation().i18n.language);
  return (
    <p>
      {copy.thanks}: {value === true ? copy.yes : value === false ? copy.no : copy.unknown}
    </p>
  );
}

export function DonationKeyNotes({
  note,
  donor,
  approval,
}: {
  note?: string;
  donor?: string;
  approval?: string | null;
}) {
  const copy = charityControlCopy(useTranslation().i18n.language);
  return (
    <span className="ops-stack">
      {note !== undefined ? (
        <span>
          {copy.keyNote}: {note || '—'}
        </span>
      ) : null}
      <span>
        {copy.donorNote}: {donor || '—'}
      </span>
      <span>
        {copy.approvalNote}: {approval || '—'}
      </span>
    </span>
  );
}

export function RecentCharitySuccess({ value }: { value?: CharitySuccess }) {
  const { i18n } = useTranslation();
  const copy = charityControlCopy(i18n.language);
  if (!value) return null;
  return (
    <div className="muted" aria-label={copy.recent}>
      <p>
        {copy.recent} · {copy.window}:{' '}
        {value.rate === null
          ? '—'
          : new Intl.NumberFormat(i18n.language, {
              style: 'percent',
              maximumFractionDigits: 1,
            }).format(value.rate)}{' '}
        {value.insufficient_sample ? `(${copy.insufficient})` : ''}
      </p>
      <p>
        {copy.samples}: {value.sample_count} · {copy.cancelled}: {value.cancelled}
      </p>
    </div>
  );
}
