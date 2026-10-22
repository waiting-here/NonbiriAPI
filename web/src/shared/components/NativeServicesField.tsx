import { useId, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  NATIVE_SERVICES,
  NATIVE_SERVICE_LABEL_KEYS,
  RERANK_FORMAT_LABEL_KEYS,
  type NativeService,
  type RerankFormat,
} from '@shared/nativeServices';
import './ModelTypesField.css';
import { Field } from './ui';

export interface NativeServicesFieldProps {
  options: readonly NativeService[];
  value: readonly NativeService[];
  onChange: (next: NativeService[]) => void;
  readOnly?: boolean;
  disabled?: boolean;
  label?: ReactNode;
  help?: ReactNode;
  error?: ReactNode;
  rerankFormats?: readonly RerankFormat[];
  rerankFormat?: RerankFormat | null;
  onRerankFormatChange?: (next: RerankFormat | null) => void;
}

export function NativeServicesField({
  options,
  value,
  onChange,
  readOnly,
  disabled,
  label,
  help,
  error,
  rerankFormats,
  rerankFormat,
  onRerankFormatChange,
}: NativeServicesFieldProps) {
  const { t } = useTranslation();
  const id = useId();
  const locked = Boolean(readOnly || disabled);
  const description = help === undefined ? t('common.nativeServices.help') : help;
  const selectionText =
    options.length === 0
      ? t('common.nativeServices.noOptions')
      : value.length === 0 && !error
        ? t('common.nativeServices.pending')
        : undefined;
  const describedBy = [
    description ? id + '-help' : undefined,
    selectionText ? id + '-selection' : undefined,
    error ? id + '-error' : undefined,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <fieldset
      className="model-types-field"
      disabled={locked}
      aria-describedby={describedBy || undefined}
      aria-invalid={error ? true : undefined}
    >
      <legend>{label ?? t('common.nativeServices.title')}</legend>
      {description ? (
        <p className="nb-sub" id={id + '-help'}>
          {description}
        </p>
      ) : null}
      <div className="nb-stack">
        <div className="model-types-field__choices">
          {options.map((service) => (
            <Field key={service} label={t(NATIVE_SERVICE_LABEL_KEYS[service])}>
              {(props) => (
                <input
                  {...props}
                  type="checkbox"
                  checked={value.includes(service)}
                  aria-readonly={readOnly || undefined}
                  onChange={(event) => {
                    if (locked) return;
                    const next = new Set(value);
                    if (event.currentTarget.checked) next.add(service);
                    else next.delete(service);
                    onChange(NATIVE_SERVICES.filter((item) => next.has(item)));
                  }}
                />
              )}
            </Field>
          ))}
        </div>
        {selectionText ? (
          <p className="nb-sub" id={id + '-selection'}>
            {selectionText}
          </p>
        ) : null}
        {value.includes('rerank') && rerankFormats && rerankFormats.length > 0 ? (
          <Field
            label={t('common.nativeServices.rerankFormat')}
            help={t('common.nativeServices.rerankFormatHelp')}
          >
            {(props) => (
              <select
                {...props}
                value={rerankFormat ?? ''}
                disabled={locked || !onRerankFormatChange}
                aria-readonly={readOnly || undefined}
                onChange={(event) => {
                  if (locked) return;
                  onRerankFormatChange?.(
                    event.currentTarget.value === ''
                      ? null
                      : (event.currentTarget.value as RerankFormat),
                  );
                }}
              >
                <option value="">{t('common.nativeServices.selectFormat')}</option>
                {rerankFormats.map((format) => (
                  <option key={format} value={format}>
                    {t(RERANK_FORMAT_LABEL_KEYS[format])}
                  </option>
                ))}
              </select>
            )}
          </Field>
        ) : null}
        {error ? (
          <p className="nb-field__error" id={id + '-error'} role="alert">
            {error}
          </p>
        ) : null}
      </div>
    </fieldset>
  );
}
