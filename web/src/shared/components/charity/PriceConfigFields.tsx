import { ModalityPricingFields } from './ModalityPricingFields';
import { RecurringDiscountFields } from './RecurringDiscountFields';
import { useRef, useState, type ReactNode, type Ref } from 'react';
import { useTranslation } from 'react-i18next';
import type { CharityRole, TokenPrices } from '@shared/operations/charity';
import { halfPrice } from '@shared/operations/charityScope';
import { Button, Field, Fields, Fold, Toggle } from '../ui';
import { TimeContextNotice } from '../TimeContext';
import { TimeInput } from '../TimeInput';
import { charityControlCopy } from '../charityControlCopy';
import { ContextPricingFields } from './ContextPricingFields';
import { charityCopyKey, tokenPriceCopyKey } from './managementFields';
import { switchPriceMode, type PriceDraft } from './priceDraft';

export function PriceConfigFields({
  value,
  role,
  onChange,
  modeRef,
  actions,
  discountError = false,
  recurringError = false,
  allowSearchUnits = true,
}: {
  value: PriceDraft;
  role: CharityRole;
  onChange: (fields: Partial<PriceDraft>) => void;
  modeRef?: Ref<HTMLSelectElement>;
  actions?: ReactNode;
  discountError?: boolean;
  recurringError?: boolean;
  allowSearchUnits?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const copy = charityControlCopy(i18n.language);
  const inputs = useRef<Record<string, HTMLInputElement | null>>({});
  const [halfError, setHalfError] = useState(false);
  const [recurringOpen, setRecurringOpen] = useState(value.recurringDiscount.enabled);
  const [discountOpen, setDiscountOpen] = useState(value.discountEnabled);
  const set = (fields: Partial<PriceDraft>) => {
    setHalfError(false);
    onChange(fields);
  };
  return (
    <div className="nb-form nb-price-config-fields">
      <fieldset className="nb-price-fields-section">
        <legend>{copy.pricingRewards}</legend>
        {actions}
        <Field label={t(charityCopyKey(role, 'pricingMode'))}>
          {(props) => (
            <select
              {...props}
              ref={modeRef}
              value={value.mode}
              onChange={(event) =>
                set(switchPriceMode(value, event.target.value as PriceDraft['mode']))
              }
            >
              <option value="per_request">{t(charityCopyKey(role, 'perRequest'))}</option>
              <option value="per_token">{t(charityCopyKey(role, 'perToken'))}</option>
              {(allowSearchUnits || value.mode === 'per_search_unit') && (
                <option value="per_search_unit">{t('common.modalityPricing.perSearchUnit')}</option>
              )}
            </select>
          )}
        </Field>
        {value.mode === 'per_token' &&
          (value.userModalities.input_enabled ||
            value.userModalities.output_enabled ||
            value.donorModalities.input_enabled ||
            value.donorModalities.output_enabled) && (
            <p className="nb-sub">{t('common.modalityPricing.fallbackHelp')}</p>
          )}
        <div className="nb-inline">
          <Button
            type="button"
            onClick={() => {
              const prices =
                value.mode !== 'per_token'
                  ? [['request', value.requestUser]]
                  : [
                      ...Object.entries(value.userPrices),
                      ...(['text', 'image', 'audio', 'video'] as const).flatMap((m) =>
                        value.userModalities[m] ? Object.entries(value.userModalities[m]) : [],
                      ),
                    ];
              const invalid = prices.find(([, amount]) => halfPrice(amount) === null);
              if (invalid) {
                setHalfError(true);
                inputs.current[invalid[0]]?.focus();
                return;
              }
              if (value.mode !== 'per_token') set({ requestDonor: halfPrice(value.requestUser)! });
              else {
                const donorRewards = { ...value.userPrices };
                for (const key of Object.keys(donorRewards) as (keyof TokenPrices)[])
                  donorRewards[key] = halfPrice(value.userPrices[key])!;
                const donorModalities = structuredClone(value.userModalities);
                for (const m of ['text', 'image', 'audio', 'video'] as const)
                  if (donorModalities[m])
                    for (const k of Object.keys(donorModalities[m]) as (keyof TokenPrices)[])
                      donorModalities[m][k] = halfPrice(donorModalities[m][k])!;
                set({ donorRewards, donorModalities });
              }
            }}
          >
            {copy.halfPrice}
          </Button>
          {halfError ? (
            <p className="field-error" role="alert">
              {copy.halfInvalid}
            </p>
          ) : null}
        </div>
        <Fold title={t('common.operations.charity.quotaReserve')}>
          <Field
            label={t('common.operations.charity.tokenReserveCredits')}
            help={t('common.operations.charity.tokenReserveCreditsHelp')}
          >
            {(props) => (
              <input
                {...props}
                inputMode="decimal"
                value={value.tokenReserveCredits ?? ''}
                onChange={(event) => set({ tokenReserveCredits: event.target.value || null })}
              />
            )}
          </Field>
          {value.mode === 'per_request' ? <p>{t('common.workflow.reserveLater')}</p> : null}
        </Fold>
        {value.mode !== 'per_token' ? (
          <Fields>
            <Field
              label={t(
                value.mode === 'per_search_unit'
                  ? 'common.modalityPricing.searchCharge'
                  : charityCopyKey(role, 'request_user_price_milli'),
              )}
            >
              {(props) => (
                <input
                  {...props}
                  inputMode="decimal"
                  value={value.requestUser}
                  ref={(node) => {
                    inputs.current.request = node;
                  }}
                  onChange={(event) => set({ requestUser: event.target.value })}
                />
              )}
            </Field>
            <Field
              label={t(
                value.mode === 'per_search_unit'
                  ? 'common.modalityPricing.searchReward'
                  : charityCopyKey(role, 'request_donor_reward_milli'),
              )}
            >
              {(props) => (
                <input
                  {...props}
                  inputMode="decimal"
                  value={value.requestDonor}
                  onChange={(event) => set({ requestDonor: event.target.value })}
                />
              )}
            </Field>
          </Fields>
        ) : (
          <div className="charity-price-grid">
            {(['userPrices', 'donorRewards'] as const).map((side) => (
              <Fields
                key={side}
                legend={t(
                  `common.operations.charity.${side === 'userPrices' ? 'userPrices' : 'donorRewards'}`,
                )}
              >
                {(Object.keys(value[side]) as (keyof TokenPrices)[]).map((field) => (
                  <Field size="short" key={field} label={t(tokenPriceCopyKey(role, side, field))}>
                    {(props) => (
                      <input
                        {...props}
                        inputMode="decimal"
                        value={value[side][field]}
                        ref={(node) => {
                          if (side === 'userPrices') inputs.current[field] = node;
                        }}
                        onChange={(event) =>
                          set({ [side]: { ...value[side], [field]: event.target.value } })
                        }
                      />
                    )}
                  </Field>
                ))}
              </Fields>
            ))}
          </div>
        )}
      </fieldset>
      {value.mode === 'per_token' && (
        <>
          <ModalityPricingFields
            title={t('common.modalityPricing.user')}
            value={value.userModalities}
            onChange={(userModalities) => set({ userModalities })}
          />
          <ModalityPricingFields
            title={t('common.modalityPricing.donor')}
            value={value.donorModalities}
            onChange={(donorModalities) => set({ donorModalities })}
          />
        </>
      )}
      <ContextPricingFields value={value} role={role} onChange={set} />
      <Fold
        title={t('common.recurringDiscount.fixedTitle')}
        summary={
          value.discountEnabled
            ? t('common.operations.charity.discountSummary', { percent: value.discountPercent })
            : t('common.disabled')
        }
        open={discountOpen || discountError}
        onOpenChange={setDiscountOpen}
      >
        <Fields>
          <TimeContextNotice station={role === 'admin' ? 'admin' : 'steward'} />
          <Toggle
            label={t(charityCopyKey(role, 'discountEnabled'))}
            checked={value.discountEnabled}
            onChange={(discountEnabled) => set({ discountEnabled })}
          />
          <Field label={t(charityCopyKey(role, 'discountPercent'))}>
            {(props) => (
              <input
                {...props}
                type="number"
                min="0"
                max="100"
                value={value.discountPercent}
                onChange={(event) => set({ discountPercent: Number(event.target.value) })}
              />
            )}
          </Field>
          <TimeInput
            label={t(charityCopyKey(role, 'discountStart'))}
            station={role === 'admin' ? 'admin' : 'steward'}
            draft={value.discountStart}
            showZoneHint={false}
            onChange={(update) => set({ discountStart: update(value.discountStart) })}
          />
          <TimeInput
            label={t(charityCopyKey(role, 'discountEnd'))}
            station={role === 'admin' ? 'admin' : 'steward'}
            draft={value.discountEnd}
            showZoneHint={false}
            onChange={(update) => set({ discountEnd: update(value.discountEnd) })}
          />
        </Fields>
      </Fold>
      <Fold
        title={t('common.recurringDiscount.title')}
        summary={
          value.recurringDiscount.enabled
            ? t('common.recurringDiscount.summary', {
                zone: value.recurringDiscount.time_zone,
                percent: value.recurringDiscount.default_percent,
                count: value.recurringDiscount.periods.length,
              })
            : t('common.disabled')
        }
        open={recurringOpen || recurringError}
        onOpenChange={setRecurringOpen}
        lazy
      >
        <RecurringDiscountFields
          value={value.recurringDiscount}
          role={role}
          fixedEnabled={value.discountEnabled}
          onChange={(recurringDiscount) => set({ recurringDiscount })}
        />
      </Fold>
    </div>
  );
}
