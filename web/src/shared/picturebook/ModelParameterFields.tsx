import { useId } from 'react';
import { parameterLabel, usePictureBookText } from './copy';
import { normalizeLines, type ParameterValues } from './parameters';
import { SizeSelector } from './SizeSelector';
import type { ImageModel, ParameterRule } from './publicTypes';

function ParameterField({
  rule,
  value,
  onChange,
}: {
  readonly rule: ParameterRule;
  readonly value: string;
  readonly onChange: (value: string) => void;
}) {
  const t = usePictureBookText();
  const id = useId();
  const label = parameterLabel(rule.key, t);
  if (rule.dimensions && !rule.enum) {
    const [width = '', height = ''] = value.split('x');
    const replaceDimension = (axis: 'width' | 'height', next: string) => {
      const parts = axis === 'width' ? [next, height] : [width, next];
      onChange(parts.every((part) => part === '') ? '' : parts.join('x'));
    };
    return (
      <fieldset className="picturebook-field">
        <legend>
          {label}
          {rule.required ? ' *' : ''}
        </legend>
        <div className="picturebook-dimensions">
          {(['width', 'height'] as const).map((axis) => {
            const range = rule.dimensions![axis];
            return (
              <div className="picturebook-field" key={axis}>
                <label htmlFor={id + '-' + axis}>
                  {axis === 'width' ? t('宽度', 'Width') : t('高度', 'Height')}
                </label>
                <input
                  id={id + '-' + axis}
                  aria-describedby={id + '-' + axis + '-range'}
                  type="number"
                  inputMode="numeric"
                  autoComplete="off"
                  required={rule.required}
                  value={axis === 'width' ? width : height}
                  min={range.minimum}
                  max={range.maximum}
                  step={range.step}
                  onChange={(event) => replaceDimension(axis, event.target.value)}
                />
                <small id={id + '-' + axis + '-range'}>
                  {range.minimum} – {range.maximum} px · {t('步长', 'Step')} {range.step}
                </small>
              </div>
            );
          })}
        </div>
      </fieldset>
    );
  }
  return (
    <div className="picturebook-field">
      <label htmlFor={id}>
        <span>
          {label}
          {rule.required ? ' *' : ''}
        </span>
      </label>
      {rule.enum ? (
        <select
          id={id}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          required={rule.required}
        >
          {!rule.required || value === '' ? (
            <option value="">{t('使用默认值', 'Use default')}</option>
          ) : null}
          {rule.enum.map((option, index) => (
            <option key={index} value={String(option)}>
              {String(option)}
            </option>
          ))}
        </select>
      ) : rule.key === 'prompt' || rule.key === 'negative_prompt' ? (
        <textarea
          id={id}
          value={value}
          autoComplete="off"
          spellCheck={false}
          required={rule.required}
          maxLength={rule.length_unit === 'utf16_units' ? rule.max_length : undefined}
          onChange={(event) => onChange(normalizeLines(event.target.value))}
        />
      ) : (
        <input
          id={id}
          value={value}
          type={rule.type === 'string' ? 'text' : 'number'}
          inputMode={
            rule.type === 'integer' ? 'numeric' : rule.type === 'number' ? 'decimal' : undefined
          }
          step={rule.step ?? (rule.type === 'integer' ? 1 : 'any')}
          min={rule.minimum}
          max={rule.maximum}
          required={rule.required}
          autoComplete="off"
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {rule.minimum !== undefined || rule.maximum !== undefined ? (
        <small>
          {t('范围', 'Range')}: {rule.minimum ?? '—'} – {rule.maximum ?? '—'}
        </small>
      ) : null}
      {rule.max_length !== undefined ? (
        <small>
          {t('长度上限', 'Length limit')}: {rule.max_length}{' '}
          {rule.length_unit === 'utf8_bytes' || !rule.length_unit
            ? t('UTF-8字节', 'UTF-8 bytes')
            : rule.length_unit === 'unicode_scalars'
              ? t('Unicode字符', 'Unicode characters')
              : t('UTF-16单位', 'UTF-16 units')}
        </small>
      ) : null}
    </div>
  );
}

/** Shared by the user submission form and the administrator's zero-charge draft preview. */
export function ModelParameterFields({
  model,
  values,
  onChange,
}: {
  readonly model: Pick<ImageModel, 'parameters' | 'size_capability'>;
  readonly values: ParameterValues;
  readonly onChange: (patch: ParameterValues) => void;
}) {
  const t = usePictureBookText();
  const common = model.parameters.filter(
    (rule) => rule.supported && ['prompt', 'n'].includes(rule.key),
  );
  const size = model.parameters.filter(
    (rule) => rule.supported && ['size', 'aspect_ratio', 'resolution'].includes(rule.key),
  );
  const advanced = model.parameters.filter(
    (rule) =>
      rule.supported && !['prompt', 'n', 'size', 'aspect_ratio', 'resolution'].includes(rule.key),
  );
  return (
    <>
      {common.map((rule) => (
        <ParameterField
          key={rule.key}
          rule={rule}
          value={values[rule.key] ?? ''}
          onChange={(value) => onChange({ [rule.key]: value })}
        />
      ))}
      {model.size_capability ? (
        <SizeSelector capability={model.size_capability} values={values} onChange={onChange} />
      ) : (
        size.map((rule) => (
          <ParameterField
            key={rule.key}
            rule={rule}
            value={values[rule.key] ?? ''}
            onChange={(value) => onChange({ [rule.key]: value })}
          />
        ))
      )}
      {advanced.length ? (
        <details>
          <summary>{t('高级参数', 'Advanced parameters')}</summary>
          {advanced.map((rule) => (
            <ParameterField
              key={rule.key}
              rule={rule}
              value={values[rule.key] ?? ''}
              onChange={(value) => onChange({ [rule.key]: value })}
            />
          ))}
        </details>
      ) : null}
    </>
  );
}
