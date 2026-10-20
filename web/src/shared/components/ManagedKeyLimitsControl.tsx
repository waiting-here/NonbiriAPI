import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  charityKeys,
  patchManagedDonationKey,
  type CharityRole,
  type ManagedDonationKey,
} from '@shared/operations/charity';
import { patchScopedDonationKey } from '@shared/operations/scopedDonationKey';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { useCharityModelScope } from './charityModelScopeContext';
import { KeyLimitFields, KeyLimitSummary } from './KeyRoutingLimits';
import { ErrorState } from './States';
import { Button } from './ui/Button';

interface LimitsChange {
  expected_revision: string;
  expected_key_limits_revision: string;
  max_concurrency?: number;
  max_rpm?: number;
}

interface LimitsBaseline {
  donationRevision: string;
  keyRevision: string | null;
  concurrency: number | null | undefined;
  rpm: number | null | undefined;
}

export interface ManagedKeyLimitsAuthority {
  item: ManagedDonationKey;
  donationRevision: string;
}

const authorityBaseline = ({
  item,
  donationRevision,
}: ManagedKeyLimitsAuthority): LimitsBaseline => ({
  donationRevision,
  keyRevision: item.key_limits_revision,
  concurrency: item.max_concurrency,
  rpm: item.max_rpm,
});

interface LimitsDraft {
  baseline: LimitsBaseline;
  concurrency: string;
  rpm: string;
}

const newDraft = (baseline: LimitsBaseline): LimitsDraft => ({
  baseline,
  concurrency: baseline.concurrency?.toString() ?? '',
  rpm: baseline.rpm?.toString() ?? '',
});

const isDirty = (draft: LimitsDraft) =>
  draft.concurrency !== (draft.baseline.concurrency?.toString() ?? '') ||
  draft.rpm !== (draft.baseline.rpm?.toString() ?? '');

const validLimit = (value: string) =>
  value.trim() !== '' &&
  Number.isInteger(Number(value)) &&
  Number(value) >= 0 &&
  Number(value) <= 2_147_483_647;

export function ManagedKeyLimitsControl({
  item,
  donation,
  role,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonationKey;
  donation: { id: string; revision: string; status: string };
  role: CharityRole;
  refresh: () => Promise<ManagedKeyLimitsAuthority>;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const modelID = useCharityModelScope();
  const incoming = useMemo<LimitsBaseline>(
    () => ({
      donationRevision: donation.revision,
      keyRevision: item.key_limits_revision,
      concurrency: item.max_concurrency,
      rpm: item.max_rpm,
    }),
    [donation.revision, item.key_limits_revision, item.max_concurrency, item.max_rpm],
  );
  const [draft, setDraft] = useState(() => newDraft(incoming));
  const [observed, setObserved] = useState(incoming);
  if (observed !== incoming) {
    setObserved(incoming);
    if (!isDirty(draft)) setDraft(newDraft(incoming));
  }
  const { baseline, concurrency, rpm } = draft;
  const [readError, setReadError] = useState<unknown>(null);
  const [reading, setReading] = useState(false);
  const save = useRetainedOperation<LimitsChange, unknown>(
    (input, key) =>
      modelID
        ? patchScopedDonationKey(modelID, donation.id, item.id, input, key)
        : patchManagedDonationKey(role, donation.id, item.id, input, key),
    async (_input, error) => {
      setReading(true);
      try {
        const authority = await refresh();
        if (error === null) setDraft(newDraft(authorityBaseline(authority)));
      } finally {
        setReading(false);
      }
    },
    charityKeys.root(role),
  );
  const error = save.error ?? readError;
  const lost =
    isUnauthorized(error) ||
    isForbidden(error) ||
    (error instanceof ApiError && error.status === 404);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const unavailable =
    donation.status !== 'approved' ||
    item.charity_state === 'ended' ||
    item.charity_state === 'expired' ||
    item.key_limits_revision === null ||
    baseline.keyRevision === null ||
    item.max_concurrency == null ||
    item.max_rpm == null;
  const unknown = save.outcome === 'unknown';
  const conflict = save.outcome === 'conflict';
  const refreshFailed = save.outcome === 'refresh-failed';
  const locked = unavailable || lost || reading || save.isPending || unknown || refreshFailed;
  const valid = validLimit(concurrency) && validLimit(rpm);
  const changed = Number(concurrency) !== baseline.concurrency || Number(rpm) !== baseline.rpm;
  const reread = async () => {
    setReading(true);
    setReadError(null);
    try {
      const authority = await refresh();
      setDraft((current) => ({ ...current, baseline: authorityBaseline(authority) }));
      save.reset();
    } catch (error) {
      setReadError(error);
    } finally {
      setReading(false);
    }
  };
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <section className="ops-stack">
      <KeyLimitSummary concurrency={item.max_concurrency} rpm={item.max_rpm} />
      <p className="muted">{t('common.keyLimits.managedHint')}</p>
      <KeyLimitFields
        concurrency={concurrency}
        rpm={rpm}
        disabled={locked}
        onConcurrency={(value) => {
          if (!conflict) save.reset();
          setDraft((current) => ({ ...current, concurrency: value }));
        }}
        onRPM={(value) => {
          if (!conflict) save.reset();
          setDraft((current) => ({ ...current, rpm: value }));
        }}
      />
      {!valid && !unavailable ? (
        <p className="field-error" role="alert">
          {t('common.keyLimits.invalid')}
        </p>
      ) : null}
      {unavailable ? <p>{t('common.keyLimits.unavailable')}</p> : null}
      {conflict ? <p role="status">{t('common.keyLimits.conflict')}</p> : null}
      {error ? <ErrorState error={error} /> : null}
      {save.refreshError ? (
        <ErrorState
          error={save.refreshError}
          onRetry={() => {
            if (refreshFailed) void save.refresh();
            else void reread();
          }}
        />
      ) : null}
      {save.isSuccess ? <p role="status">{t('common.keyLimits.saved')}</p> : null}
      <div className="ops-actions">
        <Button
          type="button"
          variant="primary"
          disabled={
            save.isPending ||
            reading ||
            refreshFailed ||
            (!unknown && (unavailable || conflict || !valid || !changed))
          }
          onClick={() => {
            if (unknown && save.variables) {
              save.mutate(save.variables);
              return;
            }
            if (!valid || !baseline.keyRevision) return;
            save.mutate({
              expected_revision: baseline.donationRevision,
              expected_key_limits_revision: baseline.keyRevision,
              ...(Number(concurrency) !== baseline.concurrency
                ? { max_concurrency: Number(concurrency) }
                : {}),
              ...(Number(rpm) !== baseline.rpm ? { max_rpm: Number(rpm) } : {}),
            });
          }}
        >
          {t(unknown ? 'common.donationReview.retrySame' : 'common.keyLimits.save')}
        </Button>
        {!unknown && !save.isPending ? (
          <Button
            type="button"
            variant="secondary"
            disabled={reading || refreshFailed}
            onClick={() => void reread()}
          >
            {t('common.keyLimits.refreshKeepDraft')}
          </Button>
        ) : null}
      </div>
    </section>
  );
}
