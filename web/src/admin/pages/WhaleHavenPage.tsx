import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Amount } from '@shared/components/Amount';
import { ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import {
  Field,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  SaveBar,
  Toggle,
} from '@shared/components/ui';
import { useOptionalToast } from '@shared/components/Toast';
import { useRetainedOperation } from '../features/operations/useRetainedOperation';
import { useGameAdminText } from '../features/games/copy';
import {
  displayValue,
  economyProblem,
  exchangePreview,
  getWhaleConfig,
  groups,
  patchWhaleConfig,
  readField,
  units,
  whaleQueryKey,
  type WhaleConfig,
  type WhalePatch,
} from '../features/games/whaleConfig';
import './whaleHaven.css';

type SwitchKey = 'enabled' | 'exchange_enabled';
interface Draft {
  base: WhaleConfig;
  switches: Partial<Record<SwitchKey, boolean>>;
  inputs: Record<string, string>;
}

function Preview({ values }: { values: Record<string, number> }) {
  const t = useGameAdminText();
  const { gross, cuts, net, grossBP, bestReturnPct } = exchangePreview(values);
  const unit = t('通用积分', 'general credits');
  const amounts = [
    [t('兑换毛额', 'Gross'), gross],
    [t('平台', 'Platform'), cuts[0]],
    [t('低保池', 'Welfare pool'), cuts[1]],
    [t('星期四池', 'Thursday pool'), cuts[2]],
    [t('净到账', 'Net credit'), net],
  ] as const;
  return (
    <Panel>
      <PanelHead
        title={t('收益预览', 'Return preview')}
        description={t(
          '按当前表单计算，每手分别向下取整三项抽成。',
          'Uses the values in this form. Each pool cut is rounded down separately per lot.',
        )}
      />
      <PanelBody>
        <dl className="whale-admin-metrics">
          <div>
            <dt>{t('每手小鲸币', 'Pearls per lot')}</dt>
            <dd>{values.lot_pearls.toLocaleString()}</dd>
          </div>
          {amounts.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>
                <Amount value={String(value)} unit={unit} />
              </dd>
            </div>
          ))}
          <div>
            <dt>{t('最高可达毛转化率', 'Attainable gross conversion')}</dt>
            <dd>{displayValue(grossBP, 'percent')}%</dd>
          </div>
          <div>
            <dt>{t('主粮最佳净回报率', 'Best net return on feed')}</dt>
            <dd>{bestReturnPct.toLocaleString(undefined, { maximumFractionDigits: 4 })}%</dd>
          </div>
        </dl>
        <p className="nb-muted nb-small">
          {t(
            '主粮按售价、产币量和实际可达到的养成加成比较。净回报含本金，按长期整手续兑估算，未计客鲸工资。',
            'Compares staple feed prices and pearl yields with attainable growth bonuses. Net return includes the initial cost and assumes repeated full-lot exchange without guest wages.',
          )}
        </p>
      </PanelBody>
    </Panel>
  );
}

export function WhaleEditor({
  authority,
  refetch,
}: {
  authority: WhaleConfig;
  refetch: () => Promise<unknown>;
}) {
  const t = useGameAdminText();
  const toast = useOptionalToast();
  const client = useQueryClient();
  const [draft, setDraft] = useState<Draft | null>(null);
  const base = draft?.base ?? authority;
  const values = { ...base.values };
  const invalid = new Set<string>();
  const patch: WhalePatch = { expected_revision: base.revision };
  const edits: Record<string, number> = {};
  const changed = new Set<string>();
  for (const field of base.fields) {
    const input = draft?.inputs[field.key];
    if (input === undefined) continue;
    const parsed = readField(input, field);
    if (parsed === null) {
      invalid.add(field.key);
      changed.add(field.key);
    } else if (parsed !== base.values[field.key]) {
      edits[field.key] = parsed;
      values[field.key] = parsed;
      changed.add(field.key);
    }
  }
  for (const key of ['enabled', 'exchange_enabled'] as const) {
    const next = draft?.switches[key];
    if (next !== undefined && next !== base[key]) {
      patch[key] = next;
      changed.add(key);
    }
  }
  if (Object.keys(edits).length) patch.values = edits;
  const problem = economyProblem(values);
  const stale = base.revision !== authority.revision;
  const save = useRetainedOperation<WhalePatch, WhaleConfig>(
    (input, key, context) => patchWhaleConfig(input, key, context.signal),
    (_input, error) => (error ? refetch() : undefined),
    whaleQueryKey,
  );
  const discard = () => {
    setDraft(null);
    save.reset();
  };
  const submit = () => {
    if (stale || !changed.size || invalid.size || problem || save.isPending) return;
    void save
      .mutateAsync(patch)
      .then((result) => {
        client.setQueryData(whaleQueryKey, result);
        setDraft(null);
        toast?.push({
          message: t('已保存鲸鱼湾小筑设置。', 'Whale Haven settings saved.'),
          tone: 'success',
        });
      })
      .catch(() => undefined);
  };
  const start = (): Draft => ({ base: authority, switches: {}, inputs: {} });
  const editSwitch = (key: SwitchKey, checked: boolean) => {
    save.reset();
    setDraft((prev) => {
      const current = prev ?? start();
      return { ...current, switches: { ...current.switches, [key]: checked } };
    });
  };
  const editInput = (key: string, value: string) => {
    save.reset();
    setDraft((prev) => {
      const current = prev ?? start();
      return { ...current, inputs: { ...current.inputs, [key]: value } };
    });
  };
  // A semantically reverted form follows later server revisions just like a fresh form.
  if (draft && !changed.size && stale) setDraft(null);
  return (
    <>
      <Panel>
        <PanelHead title={t('开放状态', 'Availability')} />
        <PanelBody>
          <Toggle
            label={t('开放鲸鱼湾小筑', 'Open Whale Haven')}
            checked={draft?.switches.enabled ?? base.enabled}
            disabled={save.isPending}
            onChange={(next) => editSwitch('enabled', next)}
            description={t(
              '同时受游戏管理中的小游戏总开关控制。',
              'Also requires the master switch in Game settings.',
            )}
          />
          <Toggle
            label={t('开放小鲸币兑换', 'Allow pearl exchange')}
            checked={draft?.switches.exchange_enabled ?? base.exchange_enabled}
            disabled={save.isPending}
            onChange={(next) => editSwitch('exchange_enabled', next)}
            description={t(
              '小筑关闭期间暂停兑换。',
              'Exchange pauses while Whale Haven is closed.',
            )}
          />
        </PanelBody>
      </Panel>
      {invalid.size || problem ? (
        <Note tone="bad">
          {problem
            ? t(...problem)
            : t(
                '请修正标出的数值，收益预览将在数值有效后更新。',
                'Correct the highlighted values to update the return preview.',
              )}
        </Note>
      ) : (
        <Preview values={values} />
      )}
      {Object.entries(groups).map(([group, names]) => {
        const fields = base.fields.filter((f) => f.group === group);
        const pending = fields.filter((f) => changed.has(f.key)).length;
        return (
          <Fold
            key={group}
            title={t(names[0], names[1])}
            defaultOpen={group === 'exchange'}
            lazy
            meta={pending ? t(`${pending} 项未保存`, `${pending} unsaved`) : undefined}
          >
            <div className="whale-admin-fields">
              {fields.map((f) => {
                const unit = t(units[f.unit][0], units[f.unit][1]);
                const input = draft?.inputs[f.key] ?? displayValue(base.values[f.key], f.unit);
                const range = displayValue(f.min, f.unit) + '–' + displayValue(f.max, f.unit);
                return (
                  <Field
                    key={f.key}
                    label={t(f.zh, f.en) + ' (' + unit + ')'}
                    help={
                      changed.has(f.key)
                        ? t('已保存', 'Saved') +
                          ' ' +
                          displayValue(base.values[f.key], f.unit) +
                          ' ' +
                          unit
                        : undefined
                    }
                    error={
                      invalid.has(f.key)
                        ? t('请输入范围内有效的数值', 'Enter a valid value in range') +
                          ' ' +
                          range +
                          ' ' +
                          unit
                        : undefined
                    }
                  >
                    {(props) => (
                      <input
                        {...props}
                        className="nb-input"
                        type="text"
                        inputMode="decimal"
                        autoComplete="off"
                        value={input}
                        maxLength={24}
                        disabled={save.isPending}
                        onChange={(e) => editInput(f.key, e.currentTarget.value)}
                      />
                    )}
                  </Field>
                );
              })}
            </div>
          </Fold>
        );
      })}
      {stale && changed.size > 0 ? (
        <Note tone="warn">
          {t(
            '其他管理员已更新设置。放弃草稿可载入最新值。',
            'Another administrator updated these settings. Discard the draft to load the latest values.',
          )}
        </Note>
      ) : null}
      {save.error ? <ErrorState error={save.error} /> : null}
      <SaveBar
        dirtyCount={changed.size}
        busy={save.isPending}
        saveDisabled={!!problem || invalid.size > 0 || stale}
        onSave={submit}
        onDiscard={discard}
        saveLabel={t('保存设置', 'Save settings')}
        discardLabel={t('放弃更改', 'Discard changes')}
        dirtyLabel={(n) => t(`${n} 项未保存`, `${n} unsaved changes`)}
      />
    </>
  );
}
export function WhaleHavenPage() {
  const t = useGameAdminText();
  const query = useQuery({ queryKey: whaleQueryKey, queryFn: getWhaleConfig, retry: false });
  return (
    <div className="page ops-page whale-admin-page">
      <PageHeader
        title={t('鲸鱼湾小筑', 'Whale Haven')}
        back={<Link to="/games">{t('返回游戏管理', 'Back to Game settings')}</Link>}
        description={t(
          '新费率和养成参数从保存时生效。',
          'New rates and growth settings take effect when saved.',
        )}
      />
      {query.isPending ? <LoadingState /> : null}
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.data ? <WhaleEditor authority={query.data} refetch={() => query.refetch()} /> : null}
    </div>
  );
}
