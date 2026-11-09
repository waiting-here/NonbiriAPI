import { useTranslation } from 'react-i18next';
import { Field } from './ui';
import { rerankURLPreview } from '@shared/rerankPath';

export function RerankURL({ value }: { value: string }) {
  const { t } = useTranslation();
  return value ? (
    <span className="nb-small nb-muted nb-stack nb-stack--sm">
      {t('common.nativeServices.rerankURL')}: <span className="read-only-value">{value}</span>
    </span>
  ) : null;
}

export function RerankPathField({
  value,
  onChange,
  baseURL,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  baseURL: string;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Field
      label={t('common.nativeServices.rerankPath')}
      optional={t('common.connections.optional')}
      help={
        <>
          {t('common.nativeServices.rerankPathHelp')}
          <RerankURL value={rerankURLPreview(baseURL, value.trim())} />
        </>
      }
    >
      {(props) => (
        <input
          {...props}
          value={value}
          placeholder="/compatible-api/v1/reranks"
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </Field>
  );
}
