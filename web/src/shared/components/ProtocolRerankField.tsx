import { useTranslation } from 'react-i18next';
import { RERANK_FORMAT_LABEL_KEYS, type RerankFormat } from '@shared/nativeServices';
import { Field, Fold } from './ui';
import { RerankPathField } from './RerankPathField';

export function ProtocolRerankField({
  formats,
  value,
  onChange,
  disabled,
  path = '',
  onPathChange,
  baseURL = '',
  pathConfigurable,
}: {
  formats?: readonly RerankFormat[];
  value: RerankFormat | null;
  onChange: (value: RerankFormat) => void;
  disabled?: boolean;
  path?: string;
  onPathChange?: (value: string) => void;
  baseURL?: string;
  pathConfigurable?: boolean;
}) {
  const { t } = useTranslation();
  const showPath = Boolean(onPathChange && (pathConfigurable ?? Boolean(formats?.length)));
  return formats?.includes('generic') || showPath ? (
    <Fold
      title={
        showPath
          ? t('common.nativeServices.rerankSettings')
          : t('common.nativeServices.rerankFormat')
      }
      defaultOpen={Boolean(path)}
    >
      {formats?.includes('generic') ? (
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
      ) : null}
      {showPath && onPathChange ? (
        <RerankPathField
          value={path}
          onChange={onPathChange}
          baseURL={baseURL}
          disabled={disabled}
        />
      ) : null}
    </Fold>
  ) : null;
}
