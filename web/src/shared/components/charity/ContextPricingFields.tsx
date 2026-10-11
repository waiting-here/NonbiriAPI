import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui/Field';
import { Toggle } from '@shared/components/ui/Toggle';
import { Fold } from '@shared/components/ui/Fold';
import type { CharityRole, TokenPrices } from '@shared/operations/charity';
import { tokenPriceCopyKey } from './managementFields';

interface ContextDraft {
  mode: 'per_request' | 'per_token';
  longEnabled: boolean;
  longThreshold: string;
  longRequest: string;
  longPrices: TokenPrices;
  penaltyEnabled: boolean;
  penaltyThreshold: string;
  penaltyFine: string;
}

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
        <Toggle
          label={t('common.operations.charity.context.long')}
          description={t('common.operations.charity.context.longHelp')}
          checked={value.longEnabled}
          onChange={(longEnabled) => onChange({ longEnabled })}
        />
        {value.longEnabled && (
          <div className="nb-grid nb-grid--2">
            <Field label={t('common.operations.charity.context.longThreshold')}>
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
                <Field key={field} label={t(tokenPriceCopyKey(role, 'userPrices', field))}>
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
          </div>
        )}
        <Toggle
          label={t('common.operations.charity.context.penalty')}
          description={t('common.operations.charity.context.penaltyHelp')}
          checked={value.penaltyEnabled}
          onChange={(penaltyEnabled) => onChange({ penaltyEnabled })}
        />
        {value.penaltyEnabled && (
          <div className="nb-grid nb-grid--2">
            <Field label={t('common.operations.charity.context.penaltyThreshold')}>
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
          </div>
        )}
      </div>
    </Fold>
  );
}
