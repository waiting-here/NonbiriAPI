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
  const key = 'common.operations.charity.context.';
  return (
    <Fold title={t(key + 'settings')} defaultOpen={value.longEnabled || value.penaltyEnabled}>
      <div className="nb-form">
        <p className="nb-sub">{t(key + 'help')}</p>
        <Toggle
          label={t(key + 'long')}
          description={t(key + 'longHelp')}
          checked={value.longEnabled}
          onChange={(longEnabled) => onChange({ longEnabled })}
        />
        {value.longEnabled && (
          <div className="nb-grid nb-grid--2">
            <Field label={t(key + 'longThreshold')}>
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
              <Field label={t(key + 'requestPrice')}>
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
          label={t(key + 'penalty')}
          description={t(key + 'penaltyHelp')}
          checked={value.penaltyEnabled}
          onChange={(penaltyEnabled) => onChange({ penaltyEnabled })}
        />
        {value.penaltyEnabled && (
          <div className="nb-grid nb-grid--2">
            <Field label={t(key + 'penaltyThreshold')}>
              {(props) => (
                <input
                  {...props}
                  inputMode="numeric"
                  value={value.penaltyThreshold}
                  onChange={(e) => onChange({ penaltyThreshold: e.target.value })}
                />
              )}
            </Field>
            <Field label={t(key + 'fine')}>
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
