import { useTranslation } from 'react-i18next';
import type {
  DonationSourceSummary,
  ManagedDonationKeySummary,
} from '@shared/operations/donationPages';
import type { CharityBindingCandidate } from '@shared/operations/charity';
import { Icon } from '../Icon';
import { Fold } from '../ui/Fold';
import { ChoiceRow } from '../ui/ChoiceRow';
import { ConnectionUnavailable } from '../ConnectionUnavailable';
import { DonationKeyNotes } from '../DonationControlFacts';
import { KeyLimitSummary } from '../KeyRoutingLimits';
import { KeyRateLimitTime } from '../KeyRateLimitTime';

export function BindingSourceChoice({
  source,
  disabled,
  onChoose,
}: {
  source: DonationSourceSummary;
  disabled: boolean;
  onChoose: () => void;
}) {
  const { t } = useTranslation();
  const endpoint = source.safe_source;
  return (
    <button
      type="button"
      className="binding-source-choice"
      data-binding-choice={source.source_key}
      disabled={disabled}
      onClick={onChoose}
    >
      <Icon name="endpoints" className="binding-choice-icon" />
      <span className="binding-choice-identity">
        <strong>
          {endpoint.kind === 'mainstream'
            ? t('common.operations.charity.mainstreamSource', { name: endpoint.name })
            : t('common.operations.charity.customSource')}
        </strong>
        <code>{endpoint.base_url}</code>
      </span>
      <span className="binding-source-counts">
        <strong>
          {t('common.operations.charity.sourceUsableKeyCount', {
            usable: source.usable_key_count,
            total: source.key_count,
          })}
        </strong>
        <span>
          {t('common.operations.charity.sourceDonationCount', { count: source.donation_count })}
        </span>
        {source.pending_donation_count !== '0' && (
          <span>
            {t('common.operations.charity.sourcePendingDonationCount', {
              count: source.pending_donation_count,
            })}
          </span>
        )}
      </span>
      <Icon name="chevron-right" />
    </button>
  );
}

export function BindingKeyChoice({
  item,
  terminal,
  disabled,
  onChoose,
}: {
  item: ManagedDonationKeySummary;
  terminal: boolean;
  disabled: boolean;
  onChoose: () => void;
}) {
  const { t } = useTranslation();
  const fingerprint = `${item.display_head}…${item.display_tail}`;
  return (
    <>
      <button
        type="button"
        className="binding-source-choice"
        data-binding-choice={item.key_id}
        disabled={disabled || terminal}
        onClick={onChoose}
      >
        <Icon name="keys" className="binding-choice-icon" />
        <span className="binding-choice-identity">
          <strong>{item.safe_note || fingerprint}</strong>
          <span>
            {t('common.operations.charity.donationNumber', { id: item.donation_id })}
            {item.safe_note && (
              <>
                {' '}
                · <code>{fingerprint}</code>
              </>
            )}
          </span>
          {terminal && <small>{t('common.operations.charity.keyUnavailable')}</small>}
        </span>
        <span className="binding-source-counts">
          <strong>{t(`common.operations.charity.charityState.${item.charity_state}`)}</strong>
          <span>
            {item.idle
              ? t('common.donationHandling.idle')
              : t('common.donationHandling.bound', { count: item.binding_count })}
          </span>
          {item.rate_limited_until !== null || item.smart_backoff ? (
            <KeyRateLimitTime until={item.rate_limited_until} smartBackoff={item.smart_backoff} />
          ) : null}
          {item.ended_reason && (
            <span>{t(`common.donationHandling.reasons.${item.ended_reason}`)}</span>
          )}
        </span>
        <Icon name="chevron-right" />
      </button>
      <Fold plain lazy className="binding-key-info" title={t('common.bindingGuide.keyDetails')}>
        <DonationKeyNotes donor={item.donation_note} approval={item.approval_note} />
        <span>
          {t('common.donationHandling.title')} ·{' '}
          {t(`common.donationHandling.states.${item.handling.state}`)}
        </span>
        <KeyLimitSummary
          concurrency={item.max_concurrency}
          rpm={item.max_rpm}
          queue={item}
          readOnly
        />
      </Fold>
    </>
  );
}

export function BindingModelChoice({
  entry,
  selected,
  disabled,
  onSelect,
  inspect,
}: {
  entry: CharityBindingCandidate;
  selected: boolean;
  disabled: boolean;
  onSelect: (checked: boolean) => void;
  inspect?: () => void;
}) {
  const { t } = useTranslation();
  const names = entry.other_bindings?.map((binding) => binding.full_name) ?? [];
  const more = BigInt(entry.other_binding_count ?? 0) - BigInt(names.length);
  return (
    <>
      <ChoiceRow
        label={entry.upstream_model_id}
        checked={selected}
        disabled={disabled}
        onChange={onSelect}
        description={
          <>
            <span>
              {entry.source_types
                .map((source) => t(`common.operations.charity.sourceType.${source}`))
                .join(' / ')}
            </span>
            {entry.current_binding_id && <span>{t('common.bindingBatch.boundHere')}</span>}
            {names.length > 0 && (
              <span>{t('common.bindingBatch.boundElsewhere', { names: names.join(' / ') })}</span>
            )}
            {more > 0n && (
              <span>{t('common.bindingBatch.moreBindings', { count: Number(more) })}</span>
            )}
          </>
        }
      />
      {!entry.capabilities.can_bind &&
        !entry.current_binding_id &&
        entry.capabilities.unavailable_reason && (
          <ConnectionUnavailable reason={entry.capabilities.unavailable_reason} />
        )}
      {more > 0n && inspect && (
        <button type="button" className="nb-btn nb-btn--ghost" onClick={inspect}>
          {t('common.keyModels.title')}
        </button>
      )}
    </>
  );
}
