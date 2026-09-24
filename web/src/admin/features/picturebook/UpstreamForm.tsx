import { useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { normalizeLines } from '@shared/picturebook/parameters';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { ApiError } from '@shared/query/http';
import { decodeAdapter, saveUpstream, type Upstream, type UpstreamInput } from './adminApi';

const genericAdapter = {
  discovery: { method: 'GET', path: '/v1/models', items_pointer: '/data', id_pointer: '/id' },
  submit: {
    method: 'POST',
    path: '/v1/images/generations',
    mapping: {
      model_pointer: '/model',
      parameters: { prompt: '/prompt', n: '/n' },
      constants: [{ pointer: '/response_format', value: 'b64_json' }],
    },
  },
  response: {
    images_pointer: '/data',
    base64_pointer: '/b64_json',
    working_states: [],
    success_states: [],
    failure_states: [],
  },
};
export function UpstreamForm({
  value,
  onSaved,
  onReload,
}: {
  readonly value: Upstream;
  readonly onSaved: (value: { revision: string }) => void;
  readonly onReload: () => void;
}) {
  const t = usePictureBookText();
  const [baseURL, setBaseURL] = useState(value.base_url),
    [secret, setSecret] = useState(''),
    [replace, setReplace] = useState(!value.secret_set);
  const [adapter, setAdapter] = useState(JSON.stringify(value.adapter ?? genericAdapter, null, 2)),
    [origins, setOrigins] = useState(value.image_origins.join('\n'));
  const [limits, setLimits] = useState({
    rpm: value.rpm === null ? '' : String(value.rpm),
    concurrency: value.concurrency === null ? '' : String(value.concurrency),
    per_user_limit: String(value.per_user_limit),
    global_limit: String(value.global_limit),
    queue_timeout_seconds: String(value.queue_timeout_seconds),
    execution_timeout_seconds: String(value.execution_timeout_seconds),
    memory_budget_mib: String(value.memory_budget_mib),
  });
  const [saved, setSaved] = useState(false);
  const [formError, setFormError] = useState<unknown>(null);
  const save = useImageOperation('admin', saveUpstream);
  const fields = [
    ['rpm', t('全部服务请求RPM上限', 'RPM limit for all service requests'), 1, 10000],
    ['concurrency', t('完整生成任务并发上限', 'Concurrent generation tasks'), 1, 32],
    ['per_user_limit', t('每人未完成任务上限', 'Unfinished tasks per user'), 1, 100],
    ['global_limit', t('全站未完成任务上限', 'Unfinished tasks across the site'), 1, 10000],
    ['queue_timeout_seconds', t('最长排队时间（秒）', 'Maximum queue wait (seconds)'), 60, 86400],
    [
      'execution_timeout_seconds',
      t('生成执行时限（秒）', 'Execution deadline (seconds)'),
      60,
      86400,
    ],
    ['memory_budget_mib', t('图片内存预算（MiB）', 'Image memory budget (MiB)'), 512, 4096],
  ] as const;
  const submit = () => {
    if (save.pending) return;
    setFormError(null);
    try {
      let input = save.input;
      if (!input) {
        if (new TextEncoder().encode(adapter).byteLength > 262144) throw new Error();
        const parsed = decodeAdapter(JSON.parse(adapter));
        const numbers = Object.fromEntries(
          fields.map(([key, , min, max]) => {
            const n = Number(limits[key]);
            if (!Number.isSafeInteger(n) || n < min || n > max) throw new Error();
            return [key, n];
          }),
        ) as Pick<UpstreamInput, keyof typeof limits>;
        input = {
          expected_revision: value.revision,
          base_url: baseURL.trim(),
          ...numbers,
          secret: replace ? { mode: 'replace', value: secret } : { mode: 'keep' },
          image_origins: normalizeLines(origins)
            .split('\n')
            .map((origin) => origin.trim())
            .filter(Boolean),
          adapter: parsed,
        };
      }
      void save.run(input, (result) => {
        setSecret('');
        setSaved(true);
        onSaved(result);
      });
    } catch {
      setFormError(
        new ApiError(
          'invalid_request',
          t(
            '请检查服务设置、数值范围及适配配置JSON。',
            'Check service settings, numeric limits and adapter JSON.',
          ),
          400,
        ),
      );
    }
  };
  return (
    <Card>
      <h2>{t('图片生成服务', 'Image generation service')}</h2>
      <p>
        {t(
          '配置只供管理员查看。密钥只写入，不回显。保存的新配置用于新提交；已受理任务继续使用其原配置。',
          'Only administrators can view this configuration. Stored keys are never returned. New settings apply to new submissions; accepted tasks retain their original configuration.',
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
            {t('服务地址', 'Service base URL')}
            <input
              type="url"
              required
              maxLength={4096}
              value={baseURL}
              onChange={(event) => setBaseURL(event.target.value)}
            />
          </label>
          {value.secret_set ? (
            <label className="picturebook-checkbox">
              <input
                type="checkbox"
                checked={replace}
                onChange={(event) => setReplace(event.target.checked)}
              />
              {t('替换已保存的密钥', 'Replace stored key')}
            </label>
          ) : null}
          {replace ? (
            <label>
              {t('活动专用密钥', 'Activity key')}
              <input
                type="password"
                autoComplete="new-password"
                required
                maxLength={65536}
                value={secret}
                onChange={(event) => setSecret(event.target.value)}
              />
            </label>
          ) : (
            <p>{t('保留已保存的密钥。', 'The existing key will be retained.')}</p>
          )}
          <div className="picturebook-grid">
            {fields.map(([key, label, min, max]) => (
              <label key={key}>
                {label}
                <input
                  type="number"
                  required
                  min={min}
                  max={max}
                  step={1}
                  value={limits[key]}
                  onChange={(event) => setLimits((old) => ({ ...old, [key]: event.target.value }))}
                />
              </label>
            ))}
          </div>
          <label>
            {t(
              '允许下载图片的来源站点（每行一个，最多8个）',
              'Allowed image download origins (one per line, up to 8)',
            )}
            <textarea
              value={origins}
              onChange={(event) => setOrigins(normalizeLines(event.target.value))}
              spellCheck={false}
            />
          </label>
          <label>
            {t('通用适配配置JSON', 'Declarative adapter JSON')}
            <textarea
              className="picturebook-json"
              value={adapter}
              spellCheck={false}
              autoComplete="off"
              onChange={(event) => setAdapter(normalizeLines(event.target.value))}
            />
          </label>
          <p>
            {t(
              '字段路径采用JSON Pointer；路径相对服务地址。可选poll配置使用GET路径及一个{task_id}占位符，并配置状态字段和工作／成功／失败状态列表。适配配置不能执行脚本。',
              'Field paths use JSON Pointer and request paths are relative to the service address. Optional poll settings use a GET path with one {task_id} placeholder plus a state pointer and working/success/failure lists. Adapters cannot execute scripts.',
            )}
          </p>
          <p>
            {t(
              '若提交回执没有状态字段，可在submit.receipt设置indicator_pointer和布尔或字符串indicator_value，并配置任务标识与轮询。同步图片结果仍可直接结算。',
              'For submissions acknowledged without a state field, submit.receipt can define indicator_pointer and a boolean or string indicator_value, alongside task identity and polling. Synchronous image results can still complete directly.',
            )}
          </p>
        </fieldset>
        {save.uncertain ? (
          <p role="status">
            {t(
              '保存结果尚未确认。请重试同一次保存。',
              'The save result is unconfirmed. Retry the same save.',
            )}
          </p>
        ) : null}
        {saved ? (
          <p role="status">
            {t(
              '已保存。正在重新读取配置；若未刷新，请点击重新读取。',
              'Saved. Reloading settings; use Reload if they do not refresh.',
            )}
          </p>
        ) : null}
        {formError || save.error ? <ErrorState error={formError ?? save.error} /> : null}
        <div className="picturebook-actions">
          <button className="btn btn-primary" type="submit" disabled={save.pending || saved}>
            {save.uncertain
              ? t('重试同一次保存', 'Retry the same save')
              : t('保存服务配置', 'Save service settings')}
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={save.locked}
            onClick={onReload}
          >
            {t('重新读取已保存配置', 'Reload saved settings')}
          </button>
        </div>
      </form>
    </Card>
  );
}
