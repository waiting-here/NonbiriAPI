import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import {
  initialValues,
  prepareSubmission,
  previewPrice,
  type ParameterValues,
} from '@shared/picturebook/parameters';
import { ModelParameterFields } from '@shared/picturebook/ModelParameterFields';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import type { ImageModel, SubmitInput } from '@shared/picturebook/publicTypes';
import { ApiError } from '@shared/query/http';
import { checkModel, saveModel, type AdminModel, type ModelInput } from './adminApi';
import { PriceFields } from './PriceFields';
import { draftPricing, initialPrices } from './prices';

export interface ModelDraftHandle {
  saveDraft: () => Promise<boolean>;
}

export const ModelEditor = forwardRef<
  ModelDraftHandle,
  {
    readonly value: AdminModel;
    readonly onSaved: (value: { id: string; revision: string }) => void;
    readonly onLocked: (locked: boolean) => void;
    readonly onDraft?: (input: ModelInput | null) => void;
    readonly onDirty?: (dirty: boolean) => void;
    readonly disabled?: boolean;
  }
>(function ModelEditor({ value, onSaved, onLocked, onDraft, onDirty, disabled = false }, ref) {
  const t = usePictureBookText();
  const [enabled, setEnabled] = useState(value.enabled);
  const [prices, setPrices] = useState(() => initialPrices(value));
  const [sampleValues, setSampleValues] = useState<ParameterValues>(() => initialValues(value));
  const [checking, setChecking] = useState(false),
    [checkResult, setCheckResult] = useState<Awaited<ReturnType<typeof checkModel>> | null>(null),
    [checkError, setCheckError] = useState<unknown>(null),
    [checkedSignature, setCheckedSignature] = useState('');
  const [saved, setSaved] = useState(false),
    [formError, setFormError] = useState<unknown>(null);
  const save = useImageOperation('admin', saveModel);
  const available =
    ['ready', 'legacy'].includes(value.capability_readiness ?? '') && !value.missing;
  const draft = useMemo((): ModelInput | null => {
    try {
      const pricing = draftPricing(prices);
      return { expected_revision: value.revision, enabled, price: pricing.default, pricing };
    } catch {
      return null;
    }
  }, [value.revision, enabled, prices]);
  const callbacks = useRef({ onDraft, onDirty });
  useEffect(() => {
    callbacks.current = { onDraft, onDirty };
  }, [onDraft, onDirty]);
  useEffect(() => onLocked(save.locked), [onLocked, save.locked]);
  const dirty =
    !saved &&
    (save.locked ||
      enabled !== value.enabled ||
      JSON.stringify(prices) !== JSON.stringify(initialPrices(value)));
  useEffect(() => {
    callbacks.current.onDraft?.(draft);
    callbacks.current.onDirty?.(dirty);
  }, [draft, dirty]);
  const previewModel: ImageModel | null =
    draft && available
      ? {
          ...value,
          price: draft.price,
          pricing: draft.pricing,
        }
      : null;
  const previewPrepared = previewModel ? prepareSubmission(previewModel, sampleValues) : null;
  const previewQuote = previewModel ? previewPrice(previewModel, sampleValues) : null;
  const currentSignature = JSON.stringify({ enabled, prices, sampleValues });
  const submit = async (): Promise<boolean> => {
    if (saved) return true;
    if (save.pending) return false;
    setFormError(null);
    if (!save.input && !draft) {
      setFormError(
        new ApiError(
          'invalid_request',
          t(
            '请检查整数价格、尺寸和档位；每项价格至少一种活动币大于0，尺寸或档位不能重复。',
            'Check whole-unit prices, sizes and tiers. Each price needs at least one positive currency, and sizes or tiers cannot repeat.',
          ),
          400,
        ),
      );
      return false;
    }
    const input = save.input ?? { id: value.id, input: draft! };
    let accepted = false;
    await save.run(input, (receipt) => {
      accepted = true;
      setSaved(true);
      onSaved(receipt);
    });
    return accepted;
  };
  useImperativeHandle(ref, () => ({ saveDraft: submit }));
  return (
    <Card>
      <h3>{t('模型开放与定价', 'Model availability and pricing')}</h3>
      <p className="picturebook-prewrap">{value.display_name || value.upstream_model_id}</p>
      {value.description ? <p className="picturebook-prewrap">{value.description}</p> : null}
      <p>
        {t(
          '支持参数和尺寸由模型目录自动提供。价格先选精确尺寸，再选档位或默认价，最后乘生成张数。',
          'Supported parameters and sizes come from the model catalog. Pricing uses exact size, then tier or default, multiplied by image count.',
        )}
      </p>
      {!available ? (
        <div role="status">
          <p>
            {t(
              '当前模型暂不可用，请刷新目录或选择其他模型。',
              'This model is currently unavailable. Refresh the catalog or choose another model.',
            )}
          </p>
          {value.capability_issues.length ? (
            <ul>
              {value.capability_issues.map((issue, index) => (
                <li key={index}>
                  {issue.code === 'unsupported_metadata'
                    ? t(
                        '服务目录中的模型信息不完整或暂不支持。',
                        'The catalog information for this model is incomplete or unsupported.',
                      )
                    : issue.safe_message}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
      <form
        className="picturebook-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={save.locked || saved || disabled}>
          <label className="picturebook-checkbox">
            <input
              type="checkbox"
              checked={enabled}
              disabled={!available && !enabled}
              onChange={(event) => setEnabled(event.target.checked)}
            />
            {t('向用户开放此模型', 'Make this model available')}
          </label>
          <PriceFields model={value} value={prices} onChange={setPrices} />
        </fieldset>
        <p>
          {t(
            '下架模型会取消其未开始任务并退款，正在执行的任务继续结算。',
            'Disabling a model cancels and refunds queued tasks; running tasks continue to settlement.',
          )}
        </p>
        {save.uncertain ? (
          <p role="status">
            {t(
              '保存结果尚未确认，请重试同一次保存。',
              'The save result is unconfirmed. Retry the same save.',
            )}
          </p>
        ) : null}
        {saved ? (
          <p role="status">
            {t(
              '模型配置已保存。正在重新读取；如未刷新，请重新读取选中模型。',
              'Model settings saved. Reloading; use Reload selected model if needed.',
            )}
          </p>
        ) : null}
        {formError || save.error ? <ErrorState error={formError ?? save.error} /> : null}
        <button
          className="nb-btn nb-btn--primary"
          type="submit"
          disabled={save.pending || saved || disabled}
        >
          {save.uncertain
            ? t('重试同一次保存', 'Retry the same save')
            : t('保存模型配置', 'Save model settings')}
        </button>
      </form>
      <details className="picturebook-form">
        <summary>
          {t('试选参数与查看价格（不扣币）', 'Try parameters and prices (no charge)')}
        </summary>
        <p>
          {t(
            '可选的本地预览，不会调用生成服务或扣费。',
            'An optional local preview that makes no generation call or charge.',
          )}
        </p>
        {previewModel ? (
          <>
            <fieldset disabled={checking || save.locked || disabled}>
              <ModelParameterFields
                model={previewModel}
                values={sampleValues}
                onChange={(patch) => setSampleValues((current) => ({ ...current, ...patch }))}
              />
            </fieldset>
            <p role="status">
              {t('本地预计整单', 'Local estimated total')}:{' '}
              {previewQuote
                ? previewQuote.total.paper + ' / ' + previewQuote.total.brush
                : t('当前组合没有可用价格', 'No price for this selection')}
            </p>
            {previewPrepared && 'problem' in previewPrepared ? (
              <p role="alert">{t('请检查预览参数。', 'Check the preview parameters.')}</p>
            ) : null}
          </>
        ) : (
          <p>
            {t(
              '填写有效价格后，可试选此模型支持的参数。',
              'Enter valid prices to try this model’s supported parameters.',
            )}
          </p>
        )}
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          disabled={checking || save.locked || disabled || !previewModel}
          onClick={() => {
            setCheckedSignature(currentSignature);
            if (!draft || !previewPrepared || 'problem' in previewPrepared) {
              setCheckError(
                new ApiError(
                  'invalid_request',
                  t('请先修正价格或预览参数。', 'Fix the prices or preview parameters first.'),
                  400,
                ),
              );
              return;
            }
            const parameters: Partial<SubmitInput> = { ...previewPrepared.input };
            delete parameters.model_id;
            delete parameters.expected_model_revision;
            delete parameters.expected_pricing_revision;
            setChecking(true);
            setCheckError(null);
            setCheckResult(null);
            void checkModel({ model_id: value.id, draft, parameters })
              .then(setCheckResult)
              .catch(setCheckError)
              .finally(() => setChecking(false));
          }}
        >
          {checking ? t('正在检查', 'Checking') : t('检查草稿', 'Check draft')}
        </button>
        {checkError && checkedSignature === currentSignature ? (
          <ErrorState error={checkError} />
        ) : null}
        {checkResult && checkedSignature === currentSignature ? (
          checkResult.valid && checkResult.quote ? (
            <p role="status">
              {t(
                '本地检查通过；未验证上游生成。',
                'Local check passed; upstream generation is unverified.',
              )}{' '}
              {t('单张', 'Per image')}: {checkResult.quote.unit.paper} /{' '}
              {checkResult.quote.unit.brush}; {t('整单', 'Total')}: {checkResult.quote.total.paper}{' '}
              / {checkResult.quote.total.brush}
            </p>
          ) : (
            <ul role="alert">
              {checkResult.issues.map((issue, index) => (
                <li key={index}>{issue.safe_message}</li>
              ))}
            </ul>
          )
        ) : null}
      </details>
    </Card>
  );
});
