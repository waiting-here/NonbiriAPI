import { MODEL_TYPES, MODEL_TYPE_PATHS, type ModelType } from '@shared/modelTypes';
import { useTranslation } from 'react-i18next';
import './ModelTypesField.css';
import { Field } from './ui';

const labelKeys = {
  chat_completions: 'common.modelTypes.chat_completions',
  embeddings: 'common.modelTypes.embeddings',
  images_generations: 'common.modelTypes.images_generations',
} as const;

export function ModelTypesField({
  value,
  onChange,
  disabled,
  showError = false,
}: {
  value: readonly ModelType[];
  onChange: (types: ModelType[]) => void;
  disabled?: boolean;
  showError?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="model-types-field" disabled={disabled}>
      <legend>{t('common.modelTypes.title')}</legend>
      <p className="nb-sub">{t('common.modelTypes.help')}</p>
      <div className="model-types-field__choices">
        {MODEL_TYPES.map((type) => (
          <Field key={type} label={t(labelKeys[type])} help={<code>{MODEL_TYPE_PATHS[type]}</code>}>
            {(props) => (
              <input
                {...props}
                type="checkbox"
                checked={value.includes(type)}
                onChange={(event) =>
                  onChange(
                    MODEL_TYPES.filter((item) =>
                      item === type ? event.target.checked : value.includes(item),
                    ),
                  )
                }
              />
            )}
          </Field>
        ))}
      </div>
      {showError && value.length === 0 ? (
        <p className="nb-field__error" role="alert">
          {t('common.modelTypes.required')}
        </p>
      ) : null}
    </fieldset>
  );
}

export function ModelTypesSummary({ value }: { value: readonly ModelType[] }) {
  const { t } = useTranslation();
  return (
    <span className="model-types-summary">
      {value.map((type) => (
        <span key={type} title={MODEL_TYPE_PATHS[type]}>
          {t(labelKeys[type])}
        </span>
      ))}
    </span>
  );
}
