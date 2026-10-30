import {
  MODEL_TYPE_CHOICES,
  MODEL_TYPE_GROUP_LABEL_KEYS,
  modelTypeGroup,
  modelTypeGroupPath,
  groupedModelTypes,
  setModelTypeGroup,
  type ModelType,
} from '@shared/modelTypes';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import './ModelTypesField.css';
import { Field } from './ui';

// Most services only need these; the rest stay one click away under “More APIs”.
const COMMON_MODEL_TYPES: ReadonlySet<ModelType> = new Set<ModelType>([
  'chat_completions',
  'responses',
  'embeddings',
  'images_generations',
]);

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
  const selectedOther = MODEL_TYPE_CHOICES.some(
    (type) => !COMMON_MODEL_TYPES.has(type) && value.some((item) => modelTypeGroup(item) === type),
  );
  const [moreOpen, setMoreOpen] = useState(selectedOther);
  const [sawOther, setSawOther] = useState(selectedOther);
  // Values that load later (editing an existing model) also open the extra APIs once.
  if (selectedOther !== sawOther) {
    setSawOther(selectedOther);
    if (selectedOther) setMoreOpen(true);
  }
  const choice = (type: ModelType) => (
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
  );
  return (
    <fieldset className="model-types-field" disabled={disabled}>
      <legend>{t('common.modelTypes.title')}</legend>
      <p className="nb-sub">{t('common.modelTypes.help')}</p>
      <div className="model-types-field__choices">
        {MODEL_TYPE_CHOICES.filter((type) => COMMON_MODEL_TYPES.has(type)).map(choice)}
      </div>
      <details
        className="model-types-field__more"
        open={moreOpen}
        onToggle={(event) => setMoreOpen(event.currentTarget.open)}
      >
        <summary>{t('common.modelTypes.more')}</summary>
        <div className="model-types-field__choices">
          {MODEL_TYPE_CHOICES.filter((type) => !COMMON_MODEL_TYPES.has(type)).map(choice)}
        </div>
      </details>
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
