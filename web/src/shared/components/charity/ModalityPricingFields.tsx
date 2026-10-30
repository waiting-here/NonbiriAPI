import { modalityNameKeys, modalityRateKeys } from '@shared/operations/modalityCopy';
import { useTranslation } from 'react-i18next';
import { Field, Fields, Fold, Toggle } from '../ui';
import { MODALITIES, type ModalityPricing, type TokenPrices } from '@shared/operations/charity';
import { emptyPrices } from './priceDraft';

export function ModalityPricingFields({
  value,
  onChange,
  title,
}: {
  value: ModalityPricing;
  onChange: (value: ModalityPricing) => void;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <Fold title={title} defaultOpen={value.input_enabled || value.output_enabled}>
      <div className="nb-form">
        <p className="nb-sub">{t('common.modalityPricing.help')}</p>
        <Toggle
          label={t('common.modalityPricing.input')}
          checked={value.input_enabled}
          onChange={(input_enabled) => onChange({ ...value, input_enabled })}
        />
        <Toggle
          label={t('common.modalityPricing.output')}
          checked={value.output_enabled}
          onChange={(output_enabled) => onChange({ ...value, output_enabled })}
        />
        {(value.input_enabled || value.output_enabled) &&
          MODALITIES.map((modality) => (
            <fieldset key={modality} className="nb-price-fields-section">
              <legend>{t(modalityNameKeys[modality])}</legend>
              <Toggle
                label={t('common.modalityPricing.configure')}
                description={t('common.modalityPricing.configureHelp')}
                checked={value[modality] !== undefined}
                onChange={(enabled) => {
                  const next = { ...value };
                  if (enabled) next[modality] = emptyPrices();
                  else delete next[modality];
                  onChange(next);
                }}
              />
              {value[modality] && (
                <Fields>
                  {(Object.keys(value[modality]) as (keyof TokenPrices)[])
                    .filter((field) =>
                      field === 'output' ? value.output_enabled : value.input_enabled,
                    )
                    .map((field) => (
                      <Field size="short" key={field} label={t(modalityRateKeys[field])}>
                        {(props) => (
                          <input
                            {...props}
                            inputMode="decimal"
                            value={value[modality]![field]}
                            onChange={(e) =>
                              onChange({
                                ...value,
                                [modality]: { ...value[modality], [field]: e.target.value },
                              })
                            }
                          />
                        )}
                      </Field>
                    ))}
                </Fields>
              )}
            </fieldset>
          ))}
      </div>
    </Fold>
  );
}
