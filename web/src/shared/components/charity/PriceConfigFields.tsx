import { useRef, useState, type ReactNode, type Ref } from 'react';
import { useTranslation } from 'react-i18next';
import type { CharityRole, TokenPrices } from '@shared/operations/charity';
import { halfPrice } from '@shared/operations/charityScope';
import { Button, Field, Fold, Toggle } from '../ui';
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
}: {
  value: PriceDraft;
  role: CharityRole;
  onChange: (fields: Partial<PriceDraft>) => void;
  modeRef?: Ref<HTMLSelectElement>;
  actions?: ReactNode;
  discountError?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const copy = charityControlCopy(i18n.language);
  const inputs = useRef<Record<string, HTMLInputElement | null>>({});
  const [halfError, setHalfError] = useState(false);
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
            </select>
          )}
        </Field>
        <div className="nb-inline">
          <Button
            type="button"
            onClick={() => {
              const prices =
                value.mode === 'per_request'
                  ? [['request', value.requestUser]]
                  : Object.entries(value.userPrices);
              const invalid = prices.find(([, amount]) => halfPrice(amount) === null);
              if (invalid) {
                setHalfError(true);
                inputs.current[invalid[0]]?.focus();
                return;
              }
              if (value.mode === 'per_request')
                set({ requestDonor: halfPrice(value.requestUser)! });
              else {
                const donorRewards = { ...value.userPrices };
                for (const key of Object.keys(donorRewards) as (keyof TokenPrices)[])
                  donorRewards[key] = halfPrice(value.userPrices[key])!;
                set({ donorRewards });
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
        {value.mode === 'per_request' ? (
          <div className="nb-grid nb-grid--2">
            <Field label={t(charityCopyKey(role, 'request_user_price_milli'))}>
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
            <Field label={t(charityCopyKey(role, 'request_donor_reward_milli'))}>
              {(props) => (
                <input
                  {...props}
                  inputMode="decimal"
                  value={value.requestDonor}
                  onChange={(event) => set({ requestDonor: event.target.value })}
                />
              )}
            </Field>
          </div>
        ) : (
          <div className="charity-price-grid">
            <div className="charity-price-headings" aria-hidden="true">
              {(['userPrices', 'donorRewards'] as const).map((side) => (
                <strong key={side}>
                  {t(
                    `common.operations.charity.${side === 'userPrices' ? 'userPrices' : 'donorRewards'}`,
                  )}
                </strong>
              ))}
            </div>
            {(Object.keys(value.userPrices) as (keyof TokenPrices)[]).map((field) => (
              <div className="charity-price-row" key={field}>
                {(['userPrices', 'donorRewards'] as const).map((side) => (
                  <Field key={side} label={t(tokenPriceCopyKey(role, side, field))}>
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
              </div>
            ))}
          </div>
        )}
      </fieldset>
      <ContextPricingFields value={value} role={role} onChange={set} />
      <Fold
        title={copy.discountSettings}
        summary={
          value.discountEnabled
            ? t('common.operations.charity.discountSummary', { percent: value.discountPercent })
            : t('common.disabled')
        }
        open={discountOpen || discountError}
        onOpenChange={setDiscountOpen}
      >
        <div className="nb-grid nb-grid--2">
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
        </div>
      </Fold>
    </div>
  );
}
