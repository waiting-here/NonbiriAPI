import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import {
  currencyUnits,
  initialValues,
  normalizeLines,
  prepareSubmission,
  previewPrice,
  type ParameterValues,
} from '@shared/picturebook/parameters';
import { ModelParameterFields } from '@shared/picturebook/ModelParameterFields';
import {
  decodeCombinations,
  decodeParameters,
  decodePricingPolicy,
  decodeSizeCapability,
} from '@shared/picturebook/publicApi';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import type { ImageModel, ParameterRule, SubmitInput } from '@shared/picturebook/publicTypes';
import { ApiError } from '@shared/query/http';
import { checkModel, decodeMapping, saveModel, type AdminModel, type ModelInput } from './adminApi';

const defaultRules: ParameterRule[] = [
  {
    key: 'prompt',
    supported: true,
    required: true,
    type: 'string',
    min_length: 1,
    max_length: 65536,
    length_unit: 'utf8_bytes',
  },
  {
    key: 'n',
    supported: true,
    required: false,
    type: 'integer',
    minimum: 1,
    maximum: 1,
    default: 1,
  },
];
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
  }
>(function ModelEditor({ value, onSaved, onLocked, onDraft, onDirty }, ref) {
  const t = usePictureBookText();
  const [name, setName] = useState(value.display_name),
    [description, setDescription] = useState(value.description),
    [enabled, setEnabled] = useState(value.enabled);
  const [paper, setPaper] = useState(value.price.paper),
    [brush, setBrush] = useState(value.price.brush);
  const [fallback, setFallback] = useState(value.pricing?.fallback ?? 'default'),
    [tiers, setTiers] = useState(JSON.stringify(value.pricing?.tiers ?? [], null, 2)),
    [sizes, setSizes] = useState(JSON.stringify(value.pricing?.sizes ?? [], null, 2)),
    [sizeCapability, setSizeCapability] = useState(
      JSON.stringify(value.size_capability ?? null, null, 2),
    ),
    [confirmed, setConfirmed] = useState(
      value.capability_readiness === 'ready' || value.capability_readiness === 'legacy',
    );
  const [parameters, setParameters] = useState(
    JSON.stringify(value.configured ? value.parameters : defaultRules, null, 2),
  );
  const [combinations, setCombinations] = useState(JSON.stringify(value.combinations, null, 2)),
    [mapping, setMapping] = useState(JSON.stringify(value.mapping, null, 2));
  const [catalogType, setCatalogType] = useState(value.catalog_type ?? 'unknown');
  const [sampleValues, setSampleValues] = useState<ParameterValues>(() =>
    initialValues({ ...value, parameters: value.configured ? value.parameters : defaultRules }),
  );
  const [checking, setChecking] = useState(false),
    [checkResult, setCheckResult] = useState<Awaited<ReturnType<typeof checkModel>> | null>(null),
    [checkError, setCheckError] = useState<unknown>(null),
    [checkedSignature, setCheckedSignature] = useState('');
  const currentSignature = JSON.stringify({
    name,
    description,
    enabled,
    paper,
    brush,
    fallback,
    tiers,
    sizes,
    sizeCapability,
    confirmed,
    parameters,
    combinations,
    mapping,
    catalogType,
    sampleValues,
  });
  const [saved, setSaved] = useState(false);
  const [formError, setFormError] = useState<unknown>(null);
  const save = useImageOperation('admin', saveModel);
  const callbacks = useRef({ onDraft, onDirty });
  useEffect(() => {
    callbacks.current = { onDraft, onDirty };
  }, [onDraft, onDirty]);
  useEffect(() => {
    onLocked(save.locked);
  }, [onLocked, save.locked]);
  const buildInput = (): ModelInput => {
    if (
      currencyUnits(paper) === null ||
      currencyUnits(brush) === null ||
      (paper === '0' && brush === '0') ||
      [parameters, combinations, mapping, tiers, sizes, sizeCapability].some(
        (v) => new TextEncoder().encode(v).byteLength > 65536,
      )
    )
      throw new Error('invalid draft');
    return {
      expected_revision: value.revision,
      display_name: name,
      description: normalizeLines(description),
      enabled,
      price: { paper, brush },
      parameters: decodeParameters(JSON.parse(parameters)),
      combinations: decodeCombinations(JSON.parse(combinations)),
      mapping: decodeMapping(JSON.parse(mapping)),
      pricing: decodePricingPolicy({
        default: { paper, brush },
        fallback,
        tiers: JSON.parse(tiers),
        sizes: JSON.parse(sizes),
      }),
      ...(JSON.parse(sizeCapability) === null
        ? {}
        : { size_capability: decodeSizeCapability(JSON.parse(sizeCapability)) }),
      capability_confirmed: confirmed,
      catalog_type: catalogType as 'image' | 'unknown' | 'other',
    };
  };
  let previewModel: ImageModel | null = null;
  try {
    const input = buildInput();
    previewModel = {
      id: value.id,
      display_name: input.display_name,
      description: input.description,
      revision: value.revision,
      pricing_revision: value.pricing_revision,
      price: input.price,
      pricing: input.pricing,
      parameters: input.parameters,
      combinations: input.combinations,
      size_capability: input.size_capability,
    };
  } catch {
    // Keep malformed draft JSON editable; the preview remains unavailable.
  }
  const previewPrepared = previewModel ? prepareSubmission(previewModel, sampleValues) : null;
  const previewQuote = previewModel ? previewPrice(previewModel, sampleValues) : null;
  useEffect(() => {
    try {
      callbacks.current.onDraft?.(buildInput());
    } catch {
      callbacks.current.onDraft?.(null);
    }
    callbacks.current.onDirty?.(
      name !== value.display_name ||
        description !== value.description ||
        enabled !== value.enabled ||
        paper !== value.price.paper ||
        brush !== value.price.brush ||
        fallback !== (value.pricing?.fallback ?? 'default') ||
        tiers !== JSON.stringify(value.pricing?.tiers ?? [], null, 2) ||
        sizes !== JSON.stringify(value.pricing?.sizes ?? [], null, 2) ||
        sizeCapability !== JSON.stringify(value.size_capability ?? null, null, 2) ||
        parameters !==
          JSON.stringify(value.configured ? value.parameters : defaultRules, null, 2) ||
        combinations !== JSON.stringify(value.combinations, null, 2) ||
        mapping !== JSON.stringify(value.mapping, null, 2) ||
        confirmed !==
          (value.capability_readiness === 'ready' || value.capability_readiness === 'legacy') ||
        catalogType !== (value.catalog_type ?? 'unknown'),
    );
    // State fields above are the complete editable draft; callbacks are ref-held.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    name,
    description,
    enabled,
    paper,
    brush,
    fallback,
    tiers,
    sizes,
    sizeCapability,
    confirmed,
    parameters,
    combinations,
    mapping,
    catalogType,
  ]);
  const submit = async (): Promise<boolean> => {
    if (saved) return true;
    if (save.pending) return false;
    setFormError(null);
    try {
      let input = save.input;
      if (!input) {
        input = { id: value.id, input: buildInput() };
      }
      let accepted = false;
      await save.run(input, (receipt) => {
        accepted = true;
        setSaved(true);
        onSaved(receipt);
      });
      return accepted;
    } catch {
      setFormError(
        new ApiError(
          'invalid_request',
          t(
            '请检查名称、整数价格及参数配置JSON。',
            'Check the display name, whole-unit prices and parameter JSON.',
          ),
          400,
        ),
      );
      return false;
    }
  };
  useImperativeHandle(ref, () => ({ saveDraft: submit }));
  return (
    <Card>
      <h3>{t('模型开放与定价', 'Model availability and pricing')}</h3>
      <p className="picturebook-prewrap">
        {t('内部模型', 'Internal model')}: {value.upstream_model_id}
      </p>
      <p>
        {t(
          '显示名称和说明会公开。请勿填入服务地址、密钥或内部模型标识。价格按精确尺寸、档位、默认价的顺序选取，再乘生成张数。',
          'Display names and descriptions are public. Do not include service addresses, keys or internal model identifiers. Pricing selects exact size, then tier, then the default, and multiplies by image count.',
        )}
      </p>
      <form
        className="picturebook-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={save.locked || saved}>
          <label>
            {t('用户显示名称', 'Public display name')}
            <input
              value={name}
              required
              maxLength={256}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label>
            {t('用户说明', 'Public description')}
            <textarea
              value={description}
              onChange={(event) => setDescription(normalizeLines(event.target.value))}
            />
          </label>
          <label className="picturebook-checkbox">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.target.checked)}
            />
            {t('向用户开放此模型', 'Make this model available')}
          </label>
          <label>
            {t('目录分类', 'Catalog type')}
            <select
              value={catalogType}
              onChange={(event) =>
                setCatalogType(event.target.value as 'image' | 'unknown' | 'other')
              }
            >
              <option value="image">{t('图像', 'Image')}</option>
              <option value="unknown">{t('未分类', 'Unknown')}</option>
              <option value="other">{t('其他类型', 'Other')}</option>
            </select>
          </label>
          <div className="picturebook-grid">
            <label>
              {t('每张草稿纸价格', 'Sketch paper per image')}
              <input
                inputMode="numeric"
                pattern="0|[1-9][0-9]*"
                maxLength={39}
                value={paper}
                required
                onChange={(event) => setPaper(event.target.value)}
              />
            </label>
            <label>
              {t('每张画笔价格', 'Brushes per image')}
              <input
                inputMode="numeric"
                pattern="0|[1-9][0-9]*"
                maxLength={39}
                value={brush}
                required
                onChange={(event) => setBrush(event.target.value)}
              />
            </label>
          </div>
          <label>
            {t('未指定尺寸价格时', 'When no size price matches')}
            <select
              value={fallback}
              onChange={(event) => setFallback(event.target.value as 'default' | 'unavailable')}
            >
              <option value="default">{t('使用默认价', 'Use default price')}</option>
              <option value="unavailable">
                {t('该尺寸不可提交', 'Make that size unavailable')}
              </option>
            </select>
          </label>
          <details>
            <summary>{t('尺寸档位与精确尺寸价格', 'Size tiers and exact-size prices')}</summary>
            <label>
              {t('档位单张价格JSON', 'Per-image tier prices JSON')}
              <textarea
                className="picturebook-json"
                value={tiers}
                spellCheck={false}
                onChange={(event) => setTiers(normalizeLines(event.target.value))}
              />
            </label>
            <label>
              {t('精确宽高单张价格JSON', 'Per-image width and height prices JSON')}
              <textarea
                className="picturebook-json"
                value={sizes}
                spellCheck={false}
                onChange={(event) => setSizes(normalizeLines(event.target.value))}
              />
            </label>
          </details>
          <label>
            {t('尺寸联动能力JSON（未知时填null）', 'Linked size capability JSON (null if unknown)')}
            <textarea
              className="picturebook-json"
              value={sizeCapability}
              spellCheck={false}
              onChange={(event) => setSizeCapability(normalizeLines(event.target.value))}
            />
          </label>
          <label className="picturebook-checkbox">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            {t(
              '已按真实模型资料核对支持参数和尺寸',
              'I verified the supported parameters and sizes against model information',
            )}
          </label>
          <label>
            {t('参数规则JSON', 'Parameter rules JSON')}
            <textarea
              className="picturebook-json"
              value={parameters}
              spellCheck={false}
              onChange={(event) => setParameters(normalizeLines(event.target.value))}
            />
          </label>
          <section aria-label={t('已保存参数来源', 'Accepted parameter sources')}>
            <h4>{t('已保存参数来源与有效约束', 'Accepted sources and effective constraints')}</h4>
            <p>
              {t(
                '这里显示当前已接受的规则；上方草稿更改须保存后才会更新。人工覆盖会在应用新来源时复核，不兼容时拒绝应用并保留原规则。',
                'These are the accepted rules; draft edits above appear here after saving. Manual overrides are checked against each new source, and an incompatible source cannot replace the accepted rules.',
              )}
            </p>
            {value.parameter_capabilities.length ? (
              <ul>
                {value.parameter_capabilities.map((capability, index) => {
                  const rule = value.parameters[index];
                  const source =
                    capability.source === 'discovered'
                      ? t('模型资料发现', 'Discovered model metadata')
                      : capability.source === 'profile'
                        ? t('连接能力配置', 'Connection profile')
                        : capability.source === 'manual'
                          ? capability.overridden
                            ? t('人工覆盖', 'Manual override')
                            : t('人工配置', 'Manual configuration')
                          : capability.source === 'legacy'
                            ? t('旧配置：来源未记录', 'Legacy configuration: origin unrecorded')
                            : t('来源未知', 'Origin unknown');
                  const support =
                    capability.support === 'supported'
                      ? t('支持', 'Supported')
                      : capability.support === 'unsupported'
                        ? t('不支持', 'Unsupported')
                        : t('支持状态未知', 'Support unknown');
                  return (
                    <li key={capability.key}>
                      <strong>{capability.key}</strong> · {source} · {support}
                      {capability.conflict ? (
                        <span role="alert">
                          {' · '}
                          {t(
                            '覆盖与已接受来源冲突；请修正后再应用刷新。',
                            'Override conflicts with the accepted source; revise it before applying a refresh.',
                          )}
                        </span>
                      ) : null}
                      {rule ? (
                        <details>
                          <summary>{t('查看有效约束', 'View effective constraints')}</summary>
                          <code>{JSON.stringify(rule)}</code>
                        </details>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p>{t('尚无已接受的参数规则。', 'No accepted parameter rules yet.')}</p>
            )}
          </section>
          <p>
            {t(
              '仅支持prompt、negative_prompt、n、size、aspect_ratio、resolution、seed、steps、guidance、quality。每项声明supported、required、type，可设置范围、步长、enum、default和长度单位。n的maximum限制单次张数（1–16）；提示词不能预设默认内容。',
              'Allowed keys: prompt, negative_prompt, n, size, aspect_ratio, resolution, seed, steps, guidance, quality. Each rule declares supported, required and type, with optional bounds, step, enum, default and length unit. The n maximum caps image count (1–16). Prompts cannot have default content.',
            )}
          </p>
          <p>
            {t(
              'size字符串可配置dimensions：format为width_height，width和height各自声明minimum、maximum、step（1–65536整数）。步长从各自最小值起算，用户填写宽高后提交为宽x高；enum、默认值和组合规则仍共同生效。',
              'A size string can declare dimensions with format width_height and separate width/height minimum, maximum and step (integers 1–65536). Each step starts at its own minimum. Users enter width and height, sent as widthxheight; enums, defaults and combinations still apply.',
            )}
          </p>
          <label>
            {t('参数组合限制JSON', 'Parameter combinations JSON')}
            <textarea
              className="picturebook-json"
              value={combinations}
              spellCheck={false}
              onChange={(event) => setCombinations(normalizeLines(event.target.value))}
            />
          </label>
          <p>
            {t(
              '每条规则含keys和allowed元组；元组中的null表示未设置。先应用默认值，同一规则任一元组满足即可，各规则须同时满足。空数组表示无额外组合限制。',
              'Each rule has keys and allowed tuples; null means absent. Defaults apply first. A tuple must match within each rule, and all rules must match. An empty array adds no combination restrictions.',
            )}
          </p>
          <label>
            {t('模型专用字段映射JSON', 'Model-specific field mapping JSON')}
            <textarea
              className="picturebook-json"
              value={mapping}
              spellCheck={false}
              onChange={(event) => setMapping(normalizeLines(event.target.value))}
            />
          </label>
        </fieldset>
        <p>
          {t(
            '留空映射（model_pointer为空、parameters为空对象、constants为空数组）会使用服务级映射。下架模型会取消其未开始任务并退款，正在执行的任务继续结算。',
            'An empty mapping (empty model_pointer, parameters object and constants array) inherits the service mapping. Removing a model cancels and refunds its queued tasks; running tasks continue to settlement.',
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
        <button className="btn btn-primary" type="submit" disabled={save.pending || saved}>
          {save.uncertain
            ? t('重试同一次保存', 'Retry the same save')
            : t('保存模型配置', 'Save model settings')}
        </button>
      </form>
      <section className="picturebook-form">
        <h4>{t('本地草稿检查与报价', 'Local draft check and quote')}</h4>
        <p>
          {t(
            '使用未保存规则和合成参数检查映射、联动与价格；不会调用上游生成或扣费。',
            'Check unsaved rules, linked sizes, mappings and prices with synthetic parameters. This makes no upstream generation call or charge.',
          )}
        </p>
        {previewModel ? (
          <>
            <fieldset disabled={checking || save.locked}>
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
              <p role="alert">
                {t('请检查预览参数：', 'Check preview parameter: ')}
                {previewPrepared.problem}
              </p>
            ) : null}
          </>
        ) : (
          <p role="alert">
            {t('请先修正模型草稿配置。', 'Fix the model draft before previewing.')}
          </p>
        )}
        <button
          className="btn btn-secondary"
          type="button"
          disabled={checking || save.locked || !previewModel}
          onClick={() => {
            setCheckedSignature(currentSignature);
            try {
              if (!previewModel || !previewPrepared || 'problem' in previewPrepared)
                throw new Error('invalid preview input');
              const draft = buildInput();
              const parameters: Partial<SubmitInput> = { ...previewPrepared.input };
              delete parameters.model_id;
              delete parameters.expected_model_revision;
              delete parameters.expected_pricing_revision;
              setChecking(true);
              setCheckError(null);
              setCheckResult(null);
              void checkModel({
                model_id: value.id,
                draft,
                parameters,
              })
                .then(setCheckResult)
                .catch(setCheckError)
                .finally(() => setChecking(false));
            } catch {
              setCheckError(
                new ApiError(
                  'invalid_request',
                  t('请先修正草稿或合成参数。', 'Fix the draft or synthetic parameters first.'),
                  400,
                ),
              );
            }
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
              / {checkResult.quote.total.brush} ({checkResult.quote.basis})
            </p>
          ) : (
            <ul role="alert">
              {checkResult.issues.map((issue, index) => (
                <li key={index}>
                  {issue.field_path}: {issue.safe_message}
                </li>
              ))}
            </ul>
          )
        ) : null}
      </section>
      <details>
        <summary>{t('仅管理员可见的发现信息', 'Discovered metadata, administrators only')}</summary>
        <pre className="picturebook-prewrap">{JSON.stringify(value.metadata, null, 2)}</pre>
      </details>
    </Card>
  );
});
