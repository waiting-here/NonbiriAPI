import { useTranslation } from 'react-i18next';
import { RERANK_FORMAT_LABEL_KEYS, type RerankFormat } from '@shared/nativeServices';
import { Field, Fold } from './ui';

export function ProtocolRerankField({
  formats,
  value,
  onChange,
  disabled,
}: {
  formats?: readonly RerankFormat[];
  value: RerankFormat | null;
  onChange: (value: RerankFormat) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return formats?.includes('generic') ? (
    <Fold title={t('common.nativeServices.rerankFormat')}>
      <Field
        label={t('common.nativeServices.rerankFormat')}
        help={t('common.nativeServices.rerankFormatHelp')}
      >
        {(props) => (
          <select
            {...props}
            value={value ?? 'generic'}
            disabled={disabled}
            onChange={(event) => onChange(event.target.value as RerankFormat)}
          >
            {formats.map((format) => (
              <option key={format} value={format}>
                {t(RERANK_FORMAT_LABEL_KEYS[format])}
              </option>
            ))}
          </select>
        )}
      </Field>
    </Fold>
  ) : null;
}
