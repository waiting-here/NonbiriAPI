import { useEffect, useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { currencyUnits, normalizeLines } from '@shared/picturebook/parameters';
import { decodeCombinations, decodeParameters } from '@shared/picturebook/publicApi';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { ApiError } from '@shared/query/http';
import { decodeMapping, saveModel, type AdminModel, type ModelInput } from './adminApi';

const defaultRules = [
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
export function ModelEditor({
  value,
  onSaved,
  onLocked,
}: {
  readonly value: AdminModel;
  readonly onSaved: (value: { id: string; revision: string }) => void;
  readonly onLocked: (locked: boolean) => void;
}) {
  const t = usePictureBookText();
  const [name, setName] = useState(value.display_name),
    [description, setDescription] = useState(value.description),
    [enabled, setEnabled] = useState(value.enabled);
  const [paper, setPaper] = useState(value.price.paper),
    [brush, setBrush] = useState(value.price.brush);
  const [parameters, setParameters] = useState(
    JSON.stringify(value.configured ? value.parameters : defaultRules, null, 2),
  );
  const [combinations, setCombinations] = useState(JSON.stringify(value.combinations, null, 2)),
    [mapping, setMapping] = useState(JSON.stringify(value.mapping, null, 2));
  const [saved, setSaved] = useState(false);
  const [formError, setFormError] = useState<unknown>(null);
  const save = useImageOperation('admin', saveModel);
  useEffect(() => {
    onLocked(save.locked);
  }, [onLocked, save.locked]);
  const submit = () => {
    if (save.pending) return;
    setFormError(null);
    try {
      let input = save.input;
      if (!input) {
        if (
          currencyUnits(paper) === null ||
          currencyUnits(brush) === null ||
          (paper === '0' && brush === '0') ||
          [parameters, combinations, mapping].some(
            (v) => new TextEncoder().encode(v).byteLength > 65536,
          )
        )
          throw new Error();
        const model: ModelInput = {
          expected_revision: value.revision,
          display_name: name,
          description: normalizeLines(description),
          enabled,
          price: { paper, brush },
          parameters: decodeParameters(JSON.parse(parameters)),
          combinations: decodeCombinations(JSON.parse(combinations)),
          mapping: decodeMapping(JSON.parse(mapping)),
        };
        input = { id: value.id, input: model };
      }
      void save.run(input, (receipt) => {
        setSaved(true);
        onSaved(receipt);
      });
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
    }
  };
  return (
    <Card>
      <h3>{t('模型开放与定价', 'Model availability and pricing')}</h3>
      <p className="picturebook-prewrap">
        {t('内部模型', 'Internal model')}: {value.upstream_model_id}
      </p>
      <p>
        {t(
          '显示名称和说明会公开。请勿填入服务地址、密钥或内部模型标识。每张价格相加收费，提交多张时按张数倍增。',
          'Display names and descriptions are public. Do not include service addresses, keys or internal model identifiers. Both currencies are charged together per image and multiplied by the requested count.',
        )}
      </p>
      <form
        className="picturebook-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
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
            {t('参数规则JSON', 'Parameter rules JSON')}
            <textarea
              className="picturebook-json"
              value={parameters}
              spellCheck={false}
              onChange={(event) => setParameters(normalizeLines(event.target.value))}
            />
          </label>
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
      <details>
        <summary>{t('仅管理员可见的发现信息', 'Discovered metadata, administrators only')}</summary>
        <pre className="picturebook-prewrap">{JSON.stringify(value.metadata, null, 2)}</pre>
      </details>
    </Card>
  );
}
