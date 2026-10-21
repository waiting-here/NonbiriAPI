import {
  MODEL_TYPE_CHOICES,
  MODEL_TYPE_GROUP_LABEL_KEYS,
  modelTypeGroup,
  modelTypeGroupPath,
  groupedModelTypes,
  setModelTypeGroup,
  type ModelType,
} from '@shared/modelTypes';
import { useTranslation } from 'react-i18next';
import './ModelTypesField.css';
import { Field } from './ui';

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
        {MODEL_TYPE_CHOICES.map((type) => (
          <Field
            key={type}
            label={t(MODEL_TYPE_GROUP_LABEL_KEYS[modelTypeGroup(type)])}
            help={<code>{modelTypeGroupPath(type)}</code>}
          >
            {(props) => (
              <input
                {...props}
                type="checkbox"
                checked={value.some((item) => modelTypeGroup(item) === type)}
                onChange={(event) => onChange(setModelTypeGroup(value, type, event.target.checked))}
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
      {groupedModelTypes(value).map((type) => (
        <span key={type} title={modelTypeGroupPath(type)}>
          {t(MODEL_TYPE_GROUP_LABEL_KEYS[modelTypeGroup(type)])}
        </span>
      ))}
    </span>
  );
}
