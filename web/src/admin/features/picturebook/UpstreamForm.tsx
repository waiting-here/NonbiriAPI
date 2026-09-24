import { useId, useState } from 'react';
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
  const helpID = useId();
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
    let problem = t(
      '请检查适配配置：必须是有效的 JSON，且符合下方接口字段格式。',
      'Check the adapter: it must be valid JSON with the documented interface fields.',
    );
    try {
      let input = save.input;
      if (!input) {
        if (new TextEncoder().encode(adapter).byteLength > 262144) throw new Error();
        const parsed = decodeAdapter(JSON.parse(adapter));
        problem = t(
          '请检查服务地址、密钥和各项限额；所有限额都必须是范围内的整数。',
          'Check the service URL, key and limits; all limits must be integers within their stated ranges.',
        );
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
      setFormError(new ApiError('invalid_request', problem, 400));
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
              aria-describedby={helpID + '-origins'}
              placeholder="https://cdn.example.com"
              value={origins}
              onChange={(event) => setOrigins(normalizeLines(event.target.value))}
              spellCheck={false}
            />
          </label>
          <p id={helpID + '-origins'} className="picturebook-help">
            {t(
              '只有服务返回图片网址时才需要填写；返回 base64 图片时可留空。例如结果是 https://cdn.example.com/images/1.png，这里填 https://cdn.example.com。每行只填协议、域名和可选端口，不填图片路径、密钥或通配符。它限定服务器可下载图片的来源，与拉取模型目录无关。',
              'Needed only when the service returns image URLs; leave blank for base64 images. For https://cdn.example.com/images/1.png, enter https://cdn.example.com. Each line contains only the scheme, host and optional port, with no image path, key or wildcard. This limits server-side image downloads and does not affect model discovery.',
            )}
          </p>
          <label>
            {t('通用适配配置JSON', 'Declarative adapter JSON')}
            <textarea
              aria-describedby={helpID + '-adapter'}
              className="picturebook-json"
              value={adapter}
              spellCheck={false}
              autoComplete="off"
              onChange={(event) => setAdapter(normalizeLines(event.target.value))}
            />
          </label>
          <div id={helpID + '-adapter'} className="picturebook-help">
            <p>
              {t(
                '这份配置告诉系统：到哪个接口拉取模型、怎样提交生成请求、从响应的哪个字段取图片。默认示例适用于常见的 OpenAI 兼容同步生图接口；其他服务应按其接口文档调整。',
                'This configuration tells the system where to list models, how to submit a generation request, and which response fields contain images. The default example is for a typical OpenAI-compatible synchronous image API; adjust it to match other providers.',
              )}
            </p>
            <details>
              <summary>{t('查看填写示例与字段说明', 'Examples and field guide')}</summary>
              <ul>
                <li>
                  <code>discovery</code>
                  {t(
                    '：模型目录。path 是请求路径；items_pointer 指向模型数组，id_pointer 指向每项的模型名。示例响应 ',
                    ': model catalog. path is the request path; items_pointer selects the model array and id_pointer selects each model ID. Example response: ',
                  )}
                  <code>{'{"data":[{"id":"image-model"}]}'}</code>
                  {t(' 对应 /data 和 /id。', ' uses /data and /id.')}
                </li>
                <li>
                  <code>submit</code>
                  {t(
                    '：生成接口与请求字段映射，常见路径为 /v1/images/generations。',
                    ': generation endpoint and request field mapping, commonly /v1/images/generations.',
                  )}
                </li>
                <li>
                  <code>response</code>
                  {t(
                    '：图片字段。base64_pointer=/b64_json 用于内嵌图片；url_pointer=/url 用于图片链接，并需填写上方来源站点。',
                    ': image fields. base64_pointer=/b64_json reads embedded images; url_pointer=/url reads image links and requires the origins above.',
                  )}
                </li>
              </ul>
              <p>
                {t(
                  '服务地址填 https://api.example.com、discovery.path 填 /v1/models 时，会请求 https://api.example.com/v1/models。以 / 开头的路径从域名根目录计算；不以 / 开头则接在服务地址的路径后。避免重复填写 v1。',
                  'With https://api.example.com and discovery.path=/v1/models, discovery requests https://api.example.com/v1/models. Paths starting with / begin at the host root; other paths append to the service URL path. Avoid duplicating v1.',
                )}
              </p>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setAdapter(JSON.stringify(genericAdapter, null, 2))}
              >
                {t('用 OpenAI 兼容示例替换配置', 'Use the OpenAI-compatible example')}
              </button>
            </details>
          </div>
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
