import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui/Field';
import { Fields } from '@shared/components/ui/Fields';
import { Toggle } from '@shared/components/ui/Toggle';
import { Fold } from '@shared/components/ui/Fold';
import type { CharityRole, TokenPrices } from '@shared/operations/charity';
import { tokenPriceCopyKey } from './managementFields';

import type { PriceDraft } from './priceDraft';
import { emptyModalities } from './priceDraft';
import { ModalityPricingFields } from './ModalityPricingFields';
type ContextDraft = PriceDraft;

export function ContextPricingFields({
  value,
  role,
  onChange,
}: {
  value: ContextDraft;
  role: CharityRole;
  onChange: (fields: Partial<ContextDraft>) => void;
}) {
  const { t } = useTranslation();
  return (
    <Fold
      title={t('common.operations.charity.context.settings')}
      defaultOpen={value.longEnabled || value.penaltyEnabled}
    >
      <div className="nb-form">
        <p className="nb-sub">{t('common.operations.charity.context.help')}</p>
        {value.mode !== 'per_search_unit' && (
          <Toggle
            label={t('common.operations.charity.context.long')}
            description={t('common.operations.charity.context.longHelp')}
            checked={value.longEnabled}
            onChange={(longEnabled) =>
              onChange(
                longEnabled
                  ? { longEnabled }
                  : {
                      longEnabled,
                      longThreshold: '',
                      longRequest: '0',
                      longPrices: {
                        uncached_input: '0',
                        cache_write_input: '0',
                        cache_read_input: '0',
                        output: '0',
                      },
                    },
              )
            }
          />
        )}
        {value.longEnabled && value.mode !== 'per_search_unit' && (
          <Fields>
            <Field size="short" label={t('common.operations.charity.context.longThreshold')}>
              {(props) => (
                <input
                  {...props}
                  inputMode="numeric"
                  value={value.longThreshold}
                  onChange={(e) => onChange({ longThreshold: e.target.value })}
                />
              )}
            </Field>
            {value.mode === 'per_request' ? (
              <Field label={t('common.operations.charity.context.requestPrice')}>
                {(props) => (
                  <input
                    {...props}
                    inputMode="decimal"
                    value={value.longRequest}
                    onChange={(e) => onChange({ longRequest: e.target.value })}
                  />
                )}
              </Field>
            ) : (
              (Object.keys(value.longPrices) as (keyof TokenPrices)[]).map((field) => (
                <Field
                  key={field}
                  label={t('common.workflow.longPriceField', {
                    field: t(tokenPriceCopyKey(role, 'userPrices', field)),
                  })}
                >
                  {(props) => (
                    <input
                      {...props}
                      inputMode="decimal"
                      value={value.longPrices[field]}
                      onChange={(e) =>
                        onChange({ longPrices: { ...value.longPrices, [field]: e.target.value } })
                      }
                    />
                  )}
                </Field>
              ))
            )}
          </Fields>
        )}
        {value.longEnabled && value.mode !== 'per_search_unit' && (
          <>
            <Toggle
              label={t('common.modalityPricing.longReward')}
              description={t('common.modalityPricing.longRewardHelp')}
              checked={value.longDonorOverride}
              onChange={(longDonorOverride) => onChange({ longDonorOverride })}
            />
            {value.longDonorOverride &&
              (value.mode === 'per_request' ? (
                <Field label={t('common.modalityPricing.longRequestReward')}>
                  {(props) => (
                    <input
                      {...props}
                      inputMode="decimal"
                      value={value.longDonorRequest}
                      onChange={(e) => onChange({ longDonorRequest: e.target.value })}
                    />
                  )}
                </Field>
              ) : (
                <Fields>
                  {(Object.keys(value.longDonorRewards) as (keyof TokenPrices)[]).map((field) => (
                    <Field key={field} label={t(tokenPriceCopyKey(role, 'donorRewards', field))}>
                      {(props) => (
                        <input
                          {...props}
                          inputMode="decimal"
                          value={value.longDonorRewards[field]}
                          onChange={(e) =>
                            onChange({
                              longDonorRewards: {
                                ...value.longDonorRewards,
                                [field]: e.target.value,
                              },
                            })
                          }
                        />
                      )}
                    </Field>
                  ))}
                </Fields>
              ))}
            {value.mode === 'per_token' && (
              <>
                <ModalityPricingFields
                  title={t('common.modalityPricing.longUser')}
                  value={value.longUserModalities}
                  onChange={(longUserModalities) => onChange({ longUserModalities })}
                />
                <Toggle
                  label={t('common.modalityPricing.longDonorOverride')}
                  description={t('common.modalityPricing.longDonorHelp')}
                  checked={value.longDonorModalities !== null}
                  onChange={(enabled) =>
                    onChange({ longDonorModalities: enabled ? emptyModalities() : null })
                  }
                />
                {value.longDonorModalities && (
                  <ModalityPricingFields
                    title={t('common.modalityPricing.longDonor')}
                    value={value.longDonorModalities}
                    onChange={(longDonorModalities) => onChange({ longDonorModalities })}
                  />
                )}
              </>
            )}
          </>
        )}
        <Toggle
          label={t('common.operations.charity.context.penalty')}
          description={t('common.operations.charity.context.penaltyHelp')}
          checked={value.penaltyEnabled}
          onChange={(penaltyEnabled) =>
            onChange(
              penaltyEnabled
                ? { penaltyEnabled }
                : { penaltyEnabled, penaltyThreshold: '', penaltyFine: '0' },
            )
          }
        />
        {value.penaltyEnabled && (
          <Fields>
            <Field size="short" label={t('common.operations.charity.context.penaltyThreshold')}>
              {(props) => (
                <input
                  {...props}
                  inputMode="numeric"
                  value={value.penaltyThreshold}
                  onChange={(e) => onChange({ penaltyThreshold: e.target.value })}
                />
              )}
            </Field>
            <Field label={t('common.operations.charity.context.fine')}>
              {(props) => (
                <input
                  {...props}
                  inputMode="decimal"
                  value={value.penaltyFine}
                  onChange={(e) => onChange({ penaltyFine: e.target.value })}
                />
              )}
            </Field>
          </Fields>
        )}
      </div>
    </Fold>
  );
}
