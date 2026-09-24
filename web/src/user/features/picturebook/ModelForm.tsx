import { useId, useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { parameterLabel, usePictureBookText } from '@shared/picturebook/copy';
import {
  initialValues,
  multipliedPrice,
  normalizeLines,
  prepareSubmission,
  type ParameterValues,
} from '@shared/picturebook/parameters';
import { submitTask } from '@shared/picturebook/publicApi';
import {
  type ImageModel,
  type ImageTask,
  type ParameterRule,
  type SubmitInput,
} from '@shared/picturebook/publicTypes';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import type { ActivityWallet } from '@shared/limitedactivities/api';
import { useImageReconcile } from './queries';

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
function Fields({
  model,
  locked,
  permitted,
  wallet,
  uncertain,
  pending,
  submit,
  error,
}: {
  readonly model: ImageModel;
  readonly locked: boolean;
  readonly permitted: boolean;
  readonly wallet?: ActivityWallet;
  readonly uncertain: boolean;
  readonly pending: boolean;
  readonly submit: (input?: SubmitInput) => void;
  readonly error: unknown;
}) {
  const t = usePictureBookText();
  const [values, setValues] = useState<ParameterValues>(() => initialValues(model)),
    [attempted, setAttempted] = useState(false);
  const prepared = prepareSubmission(model, values);
  const countRule = model.parameters.find((r) => r.key === 'n' && r.supported);
  const count = Number(values.n || countRule?.default || 1),
    total = multipliedPrice(model.price.paper, model.price.brush, count);
  const enough =
    wallet &&
    total &&
    BigInt(wallet.sketch_paper) >= BigInt(total.paper) &&
    BigInt(wallet.sketch_brush) >= BigInt(total.brush);
  const problem = 'problem' in prepared ? prepared.problem : undefined;
  return (
    <form
      className="picturebook-form"
      onSubmit={(event) => {
        event.preventDefault();
        setAttempted(true);
        if (uncertain) submit();
        else if (!problem && enough && permitted && 'input' in prepared) submit(prepared.input);
      }}
    >
      <fieldset disabled={locked}>
        {model.parameters
          .filter((rule) => rule.supported)
          .map((rule) => (
            <ParameterField
              key={rule.key}
              rule={rule}
              value={values[rule.key] ?? ''}
              onChange={(value) => setValues((old) => ({ ...old, [rule.key]: value }))}
            />
          ))}
      </fieldset>
      <dl className="picturebook-facts">
        <dt>{t('每张价格', 'Price per image')}</dt>
        <dd>
          {model.price.paper} {t('草稿纸', 'paper')} + {model.price.brush} {t('画笔', 'brushes')}
        </dd>
        <dt>{t('本次预扣', 'Reservation')}</dt>
        <dd>
          <output>
            {total
              ? total.paper +
                ' ' +
                t('草稿纸', 'paper') +
                ' + ' +
                total.brush +
                ' ' +
                t('画笔', 'brushes')
              : '—'}
          </output>
        </dd>
      </dl>
      {wallet && total && !enough ? (
        <p role="status">
          {t(
            '活动币余额不足，请先兑换。',
            'Your activity balance is insufficient. Exchange credits first.',
          )}
        </p>
      ) : null}
      {attempted && problem ? (
        <p role="alert">
          {problem === 'combination'
            ? t(
                '这些参数不能组合使用，请调整后再提交。',
                'This parameter combination is not supported.',
              )
            : problem === 'prompt_size'
              ? t('提示词或请求超过长度限制。', 'The prompt or request exceeds the length limit.')
              : problem === 'price'
                ? t('生成张数或总价超出限制。', 'The image count or total price exceeds its limit.')
                : t('请检查参数：', 'Check parameter: ') + parameterLabel(problem, t)}
        </p>
      ) : null}
      {uncertain ? (
        <p role="status">
          {t(
            '提交结果尚未确认。请重试同一次提交；内容和价格保持原样，不会重复扣币。',
            'The submission result is unconfirmed. Retry the same submission with its original content and price; it will not charge twice.',
          )}
        </p>
      ) : null}
      {error ? <ErrorState error={error} /> : null}
      <button
        className="btn btn-primary"
        type="submit"
        disabled={pending || (!uncertain && (!permitted || !wallet || !enough))}
      >
        {uncertain
          ? t('重试同一次提交', 'Retry the same submission')
          : pending
            ? t('正在提交', 'Submitting')
            : t('确认预扣并加入队列', 'Reserve currency and join queue')}
      </button>
    </form>
  );
}
export function ModelForm({
  account,
  models,
  available,
  wallet,
  onAccepted,
}: {
  readonly account: string;
  readonly models: ImageModel[];
  readonly available: boolean;
  readonly wallet?: ActivityWallet;
  readonly onAccepted: (task: ImageTask) => void;
}) {
  const t = usePictureBookText(),
    reconcile = useImageReconcile(account);
  const [snapshot, setSnapshot] = useState<ImageModel | undefined>(models[0]),
    [formGeneration, setFormGeneration] = useState(0);
  const operation = useImageOperation('steward', submitTask, reconcile);
  const latest = models.find((model) => model.id === snapshot?.id);
  const stale = !latest || latest.revision !== snapshot?.revision;
  return (
    <Card>
      <h2>{t('创作图片', 'Create images')}</h2>
      <div className="picturebook-form">
        <label>
          {t('图像模型', 'Image model')}
          <select
            disabled={operation.locked}
            value={snapshot?.id ?? ''}
            onChange={(event) =>
              setSnapshot(models.find((model) => model.id === event.target.value))
            }
          >
            {!snapshot || !latest ? (
              <option value={snapshot?.id ?? ''}>
                {t('请选择可用模型', 'Select an available model')}
              </option>
            ) : null}
            {models.map((model) => (
              <option key={model.id} value={model.id}>
                {model.display_name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {snapshot ? (
        <>
          <p className="picturebook-prewrap">{snapshot.description}</p>
          {stale ? (
            <div role="status">
              <p>
                {t(
                  '模型配置已变化，请载入最新配置后再提交。',
                  'The model configuration changed. Load the latest configuration before submitting.',
                )}
              </p>
              {latest ? (
                <button
                  className="btn btn-secondary"
                  disabled={operation.locked}
                  onClick={() => setSnapshot(latest)}
                >
                  {t('载入最新配置', 'Load latest configuration')}
                </button>
              ) : null}
            </div>
          ) : null}
          {!available ? (
            <p role="status">
              {t(
                '当前不能提交新任务。已受理任务仍可在下方查看。',
                'New submissions are unavailable. You can still view accepted tasks below.',
              )}
            </p>
          ) : null}
          <Fields
            key={snapshot.id + ':' + snapshot.revision + ':' + formGeneration}
            model={snapshot}
            locked={operation.locked}
            permitted={available && !stale}
            wallet={wallet}
            uncertain={operation.uncertain}
            pending={operation.pending}
            error={operation.error}
            submit={(input) => {
              const request = operation.input ?? input;
              if (request)
                void operation.run(request, (task) => {
                  onAccepted(task);
                  setFormGeneration((value) => value + 1);
                });
            }}
          />
        </>
      ) : (
        <p>{t('暂无可用图像模型。', 'No image models are available.')}</p>
      )}
    </Card>
  );
}
