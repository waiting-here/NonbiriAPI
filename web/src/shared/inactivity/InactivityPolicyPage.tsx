import { useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  credits,
  getPolicy,
  getRuns,
  policyOnly,
  previewPolicy,
  putPolicy,
  type AssetRule,
  type Configuration,
  type Policy,
  type Preview,
  type Runs,
} from './api';
import { PolicySummary } from './InactivityStatus';
import { PolicyAuditHistory } from './PolicyAuditHistory';
import './inactivity.css';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Affix, DataTable, Fold, Toggle, PanelFoot } from '@shared/components/ui';
import { AmountInput } from './AmountInput';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import '@shared/operations/operations.css';

function AssetEditor({
  name,
  rule,
  onChange,
  zh,
}: {
  name: string;
  rule: AssetRule | null;
  onChange: (value: AssetRule | null) => void;
  zh: boolean;
}) {
  return (
    <fieldset>
      <legend>{name}</legend>
      <Toggle
        label={zh ? '对此积分启用衰减' : 'Decay this currency'}
        checked={rule !== null}
        onChange={(enabled) =>
          onChange(enabled ? { mode: 'percent', value: '100', floor: '0' } : null)
        }
      />
      {rule && (
        <>
          <label>
            {zh ? '计算方式' : 'Calculation'}
            <select
              value={rule.mode}
              onChange={(e) =>
                onChange({
                  ...rule,
                  mode: e.target.value === 'fixed' ? 'fixed' : 'percent',
                  value: '',
                })
              }
            >
              <option value="percent">{zh ? '余额百分比' : 'Balance percentage'}</option>
              <option value="fixed">{zh ? '固定数量' : 'Fixed amount'}</option>
            </select>
          </label>
          <AmountInput
            key={rule.mode}
            zh={zh}
            percent={rule.mode === 'percent'}
            label={zh ? '每次扣减' : 'Decay per period'}
            value={rule.value}
            onChange={(value) => onChange({ ...rule, value })}
          />
          <AmountInput
            zh={zh}
            label={zh ? '保留余额' : 'Balance floor'}
            value={rule.floor}
            onChange={(floor) => onChange({ ...rule, floor })}
          />
        </>
      )}
    </fieldset>
  );
}
function Days({
  name,
  value,
  onChange,
  zh,
}: {
  zh: boolean;
  name: string;
  value: number | null;
  onChange: (value: number | null) => void;
}) {
  return (
    <label>
      {name}
      <Affix
        aria-label={name}
        unit={zh ? '天' : 'days'}
        type="number"
        min={1}
        max={36500}
        step={1}
        required
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value === '' ? null : Number(e.target.value))}
      />
    </label>
  );
}
function Editor({
  configuration,
  refresh,
  onSaved,
  onDirty,
  zh,
  locale,
}: {
  configuration: Configuration;
  refresh: () => Promise<Configuration>;
  onSaved: (value: Configuration) => void;
  onDirty: () => void;
  zh: boolean;
  locale?: string;
}) {
  const formatDateTime = useDateTimeFormatter();
  const [policy, setPolicy] = useState<Policy>(() => policyOnly(configuration));
  const [preview, setPreview] = useState<Preview>();
  const [runs, setRuns] = useState<Runs>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const form = useRef<HTMLFormElement>(null);
  const [retry, setRetry] = useState<{ policy: Policy; key: string; revision: string }>();
  const date = (at: number | null) =>
    at === null || at === 0 ? '—' : formatDateTime(at, locale === 'zh' ? 'zh' : 'en');
  const actionLabel = (value: string) =>
    ({
      decay: zh ? '积分衰减' : 'Decay',
      protection: zh ? '保护封禁' : 'Protective ban',
      none: zh ? '无处理' : 'None',
      administrator: zh ? '管理员豁免' : 'Administrator exemption',
      steward: zh ? '协管豁免' : 'Steward exemption',
      disabled: zh ? '政策未启用' : 'Disabled',
      banned: zh ? '已封禁' : 'Already banned',
    })[value] ?? value;
  const change = (next: Policy) => {
    onDirty();
    setPolicy(next);
    setPreview(undefined);
    setRetry(undefined);
    setError('');
  };
  const perform = async (action: () => Promise<void>) => {
    setPending(true);
    setError('');
    try {
      await action();
    } catch (failure) {
      if (
        failure &&
        typeof failure === 'object' &&
        'code' in failure &&
        failure.code === 'conflict'
      ) {
        setRetry(undefined);
        setError(
          zh
            ? '政策已被更新。请点击“重新读取”加载最新配置，再检查并保存。'
            : 'The policy has changed. Reload the latest configuration, review it, and save again.',
        );
        return;
      }
      setError(
        zh
          ? '操作未完成，请检查配置或重新读取。保存结果不确定时，可重试同一请求。'
          : 'The operation did not complete. Check the policy or reload. If a save result is uncertain, retry the same request.',
      );
    } finally {
      setPending(false);
    }
  };
  const save = async () => {
    const request = retry ?? {
      policy: structuredClone(policy),
      key: crypto.randomUUID(),
      revision: configuration.revision,
    };
    setRetry(request);
    const updated = await putPolicy(request.revision, request.policy, request.key);
    setRetry(undefined);
    onSaved(updated);
  };
  const validate = () => {
    if (!form.current?.reportValidity()) return false;
    if (policy.enabled && !policy.decay.enabled && !policy.protection.enabled) {
      setError(
        zh
          ? '请至少启用积分衰减或保护性封禁中的一项，再启用政策。'
          : 'Enable credit decay or protective bans before enabling the policy.',
      );
      return false;
    }
    const rules = Object.values(policy.decay.assets);
    if (
      rules.some(
        (rule) => rule && (!/^[0-9]+$/.test(rule.value) || !/^[0-9]+$/.test(rule.floor)),
      ) ||
      (policy.decay.enabled &&
        !rules.some((rule) => rule && /^[0-9]+$/.test(rule.value) && BigInt(rule.value) > 0n))
    ) {
      setError(
        zh
          ? '请在积分衰减中配置至少一种积分的有效扣减数量，并补全金额。'
          : 'Complete the currency amounts and choose a positive decay amount for at least one currency.',
      );
      return false;
    }
    return true;
  };
  return (
    <div className="inactivity-panel">
      <PageHeader
        title={zh ? '低活跃政策' : 'Inactivity policy'}
        description={
          zh
            ? '配置长期未活跃账号的积分衰减和保护性封禁。保存前可预览影响。'
            : 'Configure credit decay and protective bans for inactive accounts. Preview the impact before saving.'
        }
        actions={
          <Toggle
            label={zh ? '启用低活跃政策' : 'Enable inactivity policy'}
            checked={policy.enabled}
            disabled={pending}
            onChange={(enabled) => change({ ...policy, enabled })}
          />
        }
      />
      <p className="inactivity-intro">
        {zh
          ? '政策默认关闭。启用后系统定期执行；首次启用、重新启用或收紧规则至少给予 7 天宽限。'
          : 'Policies are disabled by default. Enabled policies run automatically. First enablement, re-enablement, and tighter rules grant at least seven days of grace.'}
      </p>
      <form
        ref={form}
        onSubmit={(e) => {
          e.preventDefault();
          if (validate()) void perform(save);
        }}
      >
        <fieldset disabled={pending}>
          <legend>{zh ? '政策配置' : 'Policy configuration'}</legend>
          <fieldset className="inactivity-section">
            <legend>{zh ? '积分衰减' : 'Credit decay'}</legend>
            <Toggle
              label={zh ? '启用积分衰减' : 'Enable credit decay'}
              checked={policy.decay.enabled}
              onChange={(enabled) => change({ ...policy, decay: { ...policy.decay, enabled } })}
            />
            {policy.decay.enabled && (
              <>
                <div className="ops-field-grid">
                  <Days
                    zh={zh}
                    name={zh ? '未活跃天数' : 'Inactive days'}
                    value={policy.decay.inactive_days}
                    onChange={(value) =>
                      change({ ...policy, decay: { ...policy.decay, inactive_days: value } })
                    }
                  />
                  <Days
                    zh={zh}
                    name={zh ? '执行周期' : 'Interval'}
                    value={policy.decay.interval_days}
                    onChange={(value) =>
                      change({ ...policy, decay: { ...policy.decay, interval_days: value } })
                    }
                  />
                </div>
                <div className="ops-grid">
                  {(['general', 'game'] as const).map((asset) => (
                    <AssetEditor
                      key={asset}
                      zh={zh}
                      name={
                        asset === 'general'
                          ? zh
                            ? '通用积分'
                            : 'General credits'
                          : zh
                            ? '游戏积分'
                            : 'Game credits'
                      }
                      rule={policy.decay.assets[asset]}
                      onChange={(rule) =>
                        change({
                          ...policy,
                          decay: {
                            ...policy.decay,
                            assets: { ...policy.decay.assets, [asset]: rule },
                          },
                        })
                      }
                    />
                  ))}
                </div>
              </>
            )}
          </fieldset>
          <fieldset>
            <legend>{zh ? '保护性永久封禁' : 'Permanent protective ban'}</legend>
            <Toggle
              label={zh ? '启用保护性封禁' : 'Enable protective bans'}
              checked={policy.protection.enabled}
              onChange={(enabled) =>
                change({ ...policy, protection: { ...policy.protection, enabled } })
              }
            />
            {policy.protection.enabled && (
              <Days
                zh={zh}
                name={zh ? '封禁前未活跃天数' : 'Inactive days before ban'}
                value={policy.protection.inactive_days}
                onChange={(value) =>
                  change({ ...policy, protection: { ...policy.protection, inactive_days: value } })
                }
              />
            )}
          </fieldset>
          <PanelFoot>
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              onClick={() =>
                validate() &&
                void perform(async () => {
                  setPreview(await previewPolicy(configuration.revision, policy));
                })
              }
            >
              {zh ? '预览匹配账号' : 'Preview accounts'}
            </button>
            <button className="nb-btn nb-btn--primary" type="submit">
              {retry ? (zh ? '重试保存' : 'Retry save') : zh ? '保存政策' : 'Save policy'}
            </button>
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              onClick={() =>
                void perform(async () => {
                  const latest = await refresh();
                  change(policyOnly(latest));
                })
              }
            >
              {zh ? '重新读取' : 'Reload'}
            </button>
          </PanelFoot>
        </fieldset>
      </form>
      {error && <p role="alert">{error}</p>}
      <Card>
        <h2>{zh ? '当前草稿摘要' : 'Draft summary'}</h2>
        <PolicySummary policy={policy} zh={zh} />
        <p>
          {zh ? '当前衰减宽限截止' : 'Current decay grace ends'}:{' '}
          {date(configuration.decay_grace_until)} ·{' '}
          {zh ? '当前封禁宽限截止' : 'Current ban grace ends'}:{' '}
          {date(configuration.protection_grace_until)}
        </p>
      </Card>
      <Fold
        title={zh ? '适用范围、活跃判定与执行规则' : 'Eligibility, activity and execution rules'}
      >
        <p>
          {zh
            ? '成功本人登录、成功 API 调用、签到／福利领取及有效游戏或活动操作计入活跃；页面刷新、轮询和被动捐赠回馈不计入。'
            : 'Successful sign-ins, successful API calls, check-ins, welfare claims and accepted game or activity actions count as activity. Page refreshes, polling and passive donation rewards do not.'}
        </p>
        <p>
          {zh
            ? '管理员、5 级和 6 级协管豁免，捐赠者不豁免。只衰减正的可用通用和游戏积分；冻结余额和活动币不受影响。封禁优先于衰减，错过多个周期最多补执行一次。保护封禁保留账号和余额，停止本人登录与 API 调用；既有公益捐赠仍可用并获得回馈。管理员解封后重新开始观察。'
            : 'Administrators and level 5/6 stewards are exempt; donors are not. Decay affects only positive available general and game credits. Frozen funds and activity currencies are excluded. Bans take priority and missed periods produce at most one charge. Protective bans retain the account and balances and block its login and API use; existing donations remain usable and receive rewards. Administrator restoration restarts observation.'}
        </p>
      </Fold>
      {preview && (
        <section>
          <h2>{zh ? '候选政策预览' : 'Candidate policy preview'}</h2>
          <p>
            {zh
              ? '每页最多 100 个账号。金额按当前余额估算，实际处理会重新检查活跃、权限和余额。此预览不会执行处罚。'
              : 'Up to 100 accounts per page. Amounts use current balances; processing rechecks activity, roles, and balances. Preview applies no penalties.'}
          </p>
          <p>
            {zh ? '预览衰减／封禁宽限截止' : 'Proposed decay / ban grace ends'}:{' '}
            {date(preview.configuration.decay_grace_until)} /{' '}
            {date(preview.configuration.protection_grace_until)}
          </p>
          <DataTable
            caption={zh ? '候选政策预览' : 'Candidate policy preview'}
            rows={preview.data}
            rowKey={(item) => item.user_id}
            columns={[
              {
                key: 'account',
                header: zh ? '账号' : 'Account',
                cell: 'title',
                render: (item) => item.user_id,
              },
              {
                key: 'action',
                header: zh ? '动作／豁免' : 'Action / exemption',
                cell: 'status',
                render: (item) => actionLabel(item.exempt_reason || item.action),
              },
              {
                key: 'date',
                header: zh ? '预计日期' : 'Projected date',
                cell: 'meta',
                mobileLabel: zh ? '预计日期' : 'Projected date',
                render: (item) => date(item.scheduled_at),
              },
              {
                key: 'credits',
                header: zh ? '通用／游戏积分' : 'General / game credits',
                cell: 'meta',
                mobileLabel: zh ? '通用／游戏积分' : 'General / game credits',
                render: (item) => `${credits(item.general_milli)} / ${credits(item.game_milli)}`,
              },
            ]}
          />
          {preview.next_cursor && (
            <button
              className="nb-btn nb-btn--secondary"
              disabled={pending}
              onClick={() =>
                void perform(async () => {
                  setPreview(
                    await previewPolicy(
                      configuration.revision,
                      policy,
                      preview.next_cursor ?? undefined,
                    ),
                  );
                })
              }
            >
              {zh ? '下一页预览' : 'Next preview page'}
            </button>
          )}
        </section>
      )}
      <details
        className="nb-fold"
        onToggle={(event) => {
          if (event.currentTarget.open && !runs && !pending)
            void perform(async () => {
              setRuns(await getRuns());
            });
        }}
      >
        <summary>
          <span className="nb-fold__title">
            <strong>{zh ? '执行记录' : 'Execution records'}</strong>
          </span>
        </summary>
        <div className="nb-fold__body">
          {runs && (
            <>
              <DataTable
                caption={zh ? '执行记录' : 'Execution records'}
                rows={runs.data}
                rowKey={(item) => item.id}
                columns={[
                  {
                    key: 'account',
                    header: zh ? '账号' : 'Account',
                    cell: 'title',
                    render: (item) => item.user_id ?? (zh ? '已去标识' : 'Deidentified'),
                  },
                  {
                    key: 'action',
                    header: zh ? '动作' : 'Action',
                    cell: 'status',
                    render: (item) => actionLabel(item.action),
                  },
                  {
                    key: 'date',
                    header: zh ? '时间' : 'Time',
                    cell: 'meta',
                    mobileLabel: zh ? '时间' : 'Time',
                    render: (item) => date(item.created_at),
                  },
                  {
                    key: 'credits',
                    header: zh ? '通用／游戏积分' : 'General / game credits',
                    cell: 'meta',
                    mobileLabel: zh ? '通用／游戏积分' : 'General / game credits',
                    render: (item) =>
                      `${credits(item.general_milli)} / ${credits(item.game_milli)}`,
                  },
                ]}
              />
              {runs.next_cursor && (
                <button
                  className="nb-btn nb-btn--secondary"
                  disabled={pending}
                  onClick={() =>
                    void perform(async () => {
                      setRuns(await getRuns(runs.next_cursor ?? undefined));
                    })
                  }
                >
                  {zh ? '下一页记录' : 'Next records page'}
                </button>
              )}
            </>
          )}
        </div>
      </details>
      <PolicyAuditHistory zh={zh} locale={locale} />
    </div>
  );
}
export function InactivityPolicyPage() {
  const { i18n } = useTranslation();
  const zh = Boolean(i18n.resolvedLanguage?.startsWith('zh'));
  const client = useQueryClient();
  const [saved, setSaved] = useState(false);
  const [reloadID, setReloadID] = useState(0);
  const query = useQuery({
    queryKey: ['admin-inactivity-policy'],
    queryFn: ({ signal }) => getPolicy(signal),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });
  return (
    <div className="page ops-page inactivity-panel inactivity-editor">
      {query.isPending || query.isError ? (
        <PageHeader
          title={zh ? '低活跃政策' : 'Inactivity policy'}
          description={
            zh
              ? '配置长期未活跃账号的积分衰减和保护性封禁。保存前可预览影响。'
              : 'Configure credit decay and protective bans for inactive accounts. Preview the impact before saving.'
          }
        />
      ) : null}
      {saved && <p role="status">{zh ? '政策已保存。' : 'Policy saved.'}</p>}
      {query.isPending ? (
        <LoadingState />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <Editor
          key={`${query.data.revision}/${reloadID}`}
          configuration={query.data}
          refresh={async () => {
            const result = await query.refetch({ throwOnError: true });
            setSaved(false);
            setReloadID((value) => value + 1);
            return result.data!;
          }}
          onDirty={() => setSaved(false)}
          onSaved={(value) => {
            client.setQueryData(['admin-inactivity-policy'], value);
            setSaved(true);
          }}
          zh={zh}
          locale={i18n.resolvedLanguage}
        />
      )}
    </div>
  );
}
