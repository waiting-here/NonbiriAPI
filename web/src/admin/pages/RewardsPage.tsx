import { rewardLabels, conditionSummary, assetName } from '@shared/rewards/copy';
import { useState } from 'react';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Field,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
  Toggle,
} from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ErrorState, EmptyState, LoadingState, PageHeader } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import { operationKey } from '@shared/operations/api';
import { ConditionEditor } from '@shared/rewards/ConditionEditor';
import { RewardSummary } from '@shared/rewards/RewardSummary';
import {
  createReward,
  getAssets,
  listCampaigns,
  previewReward,
  retryReward,
  rewardKeys,
  updateReward,
  type Campaign,
  type Input,
  type Preview,
} from '@shared/rewards/api';
import { DeliveryFailures } from '@shared/rewards/DeliveryFailures';
import { validRewardWindow } from '@shared/rewards/form';
import { useAdminSession } from '../data';
import '@shared/rewards/rewards.css';

const initial = (): Input => ({
  kind: 'broadcast',
  title: '',
  enabled: true,
  rewards: [{ asset: 'general', quantity: '100' }],
  conditions: { op: 'all' },
});
export function RewardsPage() {
  const session = useAdminSession();
  if (session.isPending) return <LoadingState />;
  if (session.error || !session.data) return <ErrorState error={session.error} />;
  return <RewardsContent key={session.data.admin.username} />;
}
function RewardsContent() {
  const { t, i18n } = useTranslation();
  const english = i18n.language.startsWith('en');
  const client = useQueryClient();
  const time = useDateTimeFormatter();
  const [draft, setDraft] = useState(initial);
  const [start, setStart] = useState(() => createTimeDraft(null));
  const [end, setEnd] = useState(() => createTimeDraft(null));
  const [quota, setQuota] = useState('');
  const [cursor, setCursor] = useState('');
  const [pages, setPages] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [created, setCreated] = useState<Campaign>();
  const [editing, setEditing] = useState<Campaign>();
  const [pending, setPending] = useState<{ input: Input; key: string; preview: Preview }>();
  const assetsQuery = useQuery({
    queryKey: [...rewardKeys.admin, 'assets'],
    queryFn: ({ signal }) => getAssets(true, signal),
  });
  const list = useQuery({
    queryKey: [...rewardKeys.admin, 'list', cursor],
    queryFn: ({ signal }) => listCampaigns(cursor, signal),
    refetchInterval: (q) => (q.state.data?.items.some((c) => c.pending > 0) ? 2000 : false),
  });
  const assets = assetsQuery.data ?? [];
  const windowValid = validRewardWindow(quota, start, end);
  const refresh = () => client.invalidateQueries({ queryKey: rewardKeys.admin });
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError(undefined);
    try {
      await action();
      await refresh();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  const prepare = () =>
    run(async () => {
      if (draft.kind === 'code' && !windowValid) return;
      const input: Input = {
        ...draft,
        ...(draft.kind === 'code'
          ? {
              quota: quota === '' ? undefined : Number(quota),
              starts_at: timeDraftValue(start) ?? undefined,
              ends_at: timeDraftValue(end) ?? undefined,
            }
          : { code: undefined, quota: undefined, starts_at: undefined, ends_at: undefined }),
      };
      const preview = await previewReward(input);
      setPending({ input, key: operationKey(), preview });
    });
  const confirm = () =>
    run(async () => {
      if (!pending) return;
      const c = await createReward(pending.input, pending.key);
      setCreated(c);
      setPending(undefined);
      setDraft(initial());
      setQuota('');
      setStart(createTimeDraft(null));
      setEnd(createTimeDraft(null));
      setCursor('');
      setPages([]);
    });
  if (assetsQuery.isPending) return <LoadingState />;
  if (assetsQuery.error)
    return <ErrorState error={assetsQuery.error} onRetry={() => void assetsQuery.refetch()} />;
  return (
    <div className="reward-stack">
      <PageHeader
        title={t('common.rewards.adminTitle')}
        description={t('common.rewards.adminIntro')}
      />
      {!!error && <ErrorState error={error} />}
      {created && (
        <Note tone="ok" title={t('common.rewards.created')}>
          {created.kind === 'code' ? (
            <div className="reward-code">
              <code>{created.code}</code>
              <Button
                type="button"
                onClick={() => void run(() => navigator.clipboard.writeText(created.code ?? ''))}
              >
                {t('common.rewards.copyCode')}
              </Button>
            </div>
          ) : (
            t('common.rewards.deliveryQueued', { amount: formatCount(created.matched).display })
          )}
        </Note>
      )}
      <Panel>
        <PanelHead title={t('common.rewards.configure')} />
        <PanelBody>
          <form
            className="reward-form"
            onSubmit={(e) => {
              e.preventDefault();
              void prepare();
            }}
          >
            <Tabs
              label={t('common.rewards.kind')}
              value={draft.kind}
              tabs={[
                { value: 'broadcast', label: t('common.rewards.broadcast') },
                { value: 'code', label: t('common.rewards.code') },
              ]}
              onChange={(kind) => setDraft({ ...draft, kind })}
            />
            <Field label={t('common.rewards.title')}>
              {(props) => (
                <input
                  {...props}
                  required
                  maxLength={80}
                  value={draft.title}
                  onChange={(e) => setDraft({ ...draft, title: e.target.value })}
                />
              )}
            </Field>
            <div className="reward-stack">
              {draft.rewards.map((item, index) => (
                <div className="reward-item" key={index}>
                  <Field label={t('common.rewards.asset')}>
                    {(props) => (
                      <select
                        {...props}
                        value={item.asset}
                        onChange={(e) =>
                          setDraft({
                            ...draft,
                            rewards: draft.rewards.map((r, i) =>
                              i === index ? { asset: e.target.value, quantity: '1' } : r,
                            ),
                          })
                        }
                      >
                        {assets
                          .filter(
                            (a) =>
                              a.id === item.asset || !draft.rewards.some((r) => r.asset === a.id),
                          )
                          .map((a) => (
                            <option key={a.id} value={a.id}>
                              {assetName(assets, a.id, english)}
                            </option>
                          ))}
                      </select>
                    )}
                  </Field>
                  <Field label={t('common.rewards.quantity')}>
                    {(props) => (
                      <input
                        {...props}
                        required
                        inputMode="decimal"
                        value={item.quantity}
                        onChange={(e) =>
                          setDraft({
                            ...draft,
                            rewards: draft.rewards.map((r, i) =>
                              i === index ? { ...r, quantity: e.target.value } : r,
                            ),
                          })
                        }
                      />
                    )}
                  </Field>
                  <Button
                    type="button"
                    disabled={draft.rewards.length === 1}
                    onClick={() =>
                      setDraft({ ...draft, rewards: draft.rewards.filter((_, i) => i !== index) })
                    }
                  >
                    {t('common.rewards.remove')}
                  </Button>
                </div>
              ))}
              <div>
                <Button
                  type="button"
                  disabled={draft.rewards.length >= 32}
                  onClick={() => {
                    const a = assets.find((a) => !draft.rewards.some((r) => r.asset === a.id));
                    if (a)
                      setDraft({
                        ...draft,
                        rewards: [...draft.rewards, { asset: a.id, quantity: '1' }],
                      });
                  }}
                >
                  {t('common.rewards.addReward')}
                </Button>
              </div>
            </div>
            <Fold title={t('common.rewards.conditions')}>
              <p>{t('common.rewards.conditionHelp')}</p>
              <ConditionEditor
                value={draft.conditions}
                onChange={(conditions) => setDraft({ ...draft, conditions })}
                assets={assets}
              />
            </Fold>
            <p className="reward-summary-text">
              {conditionSummary(draft.conditions, assets, english, t, time)}
            </p>
            {draft.kind === 'code' && (
              <>
                <div className="reward-fields">
                  <Field label={t('common.rewards.codeText')} help={t('common.rewards.codeHelp')}>
                    {(props) => (
                      <input
                        {...props}
                        maxLength={64}
                        autoComplete="off"
                        value={draft.code ?? ''}
                        onChange={(e) => setDraft({ ...draft, code: e.target.value })}
                      />
                    )}
                  </Field>
                  <Field label={t('common.rewards.quota')} help={t('common.rewards.optionalLimit')}>
                    {(props) => (
                      <input
                        {...props}
                        inputMode="numeric"
                        value={quota}
                        onChange={(e) => setQuota(e.target.value)}
                      />
                    )}
                  </Field>
                  <TimeInput
                    label={t('common.rewards.starts')}
                    station="admin"
                    draft={start}
                    onChange={setStart}
                  />
                  <TimeInput
                    label={t('common.rewards.ends')}
                    station="admin"
                    draft={end}
                    onChange={setEnd}
                  />
                </div>
                <Toggle
                  label={t('common.rewards.enabled')}
                  checked={draft.enabled}
                  onChange={(enabled) => setDraft({ ...draft, enabled })}
                />
              </>
            )}
            {draft.kind === 'code' && !windowValid && (
              <Note tone="warn">{t('common.rewards.invalidWindow')}</Note>
            )}
            <Note>
              {draft.kind === 'broadcast'
                ? t('common.rewards.broadcastHelp')
                : t('common.rewards.codeRule')}
            </Note>
            <div>
              <Button
                type="submit"
                variant="primary"
                disabled={busy || (draft.kind === 'code' && !windowValid)}
              >
                {t('common.rewards.preview')}
              </Button>
            </div>
          </form>
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHead
          title={t('common.rewards.management')}
          actions={
            <Button type="button" disabled={busy} onClick={() => void list.refetch()}>
              {t('common.rewards.refresh')}
            </Button>
          }
        />
        <PanelBody>
          {list.isPending ? (
            <LoadingState />
          ) : list.error ? (
            <ErrorState error={list.error} onRetry={() => void list.refetch()} />
          ) : list.data?.items.length === 0 ? (
            <EmptyState
              title={t('common.rewards.noCampaigns')}
              body={t('common.rewards.noCampaignsHelp')}
            />
          ) : (
            <div className="reward-list">
              {list.data?.items.map((c) => (
                <article className="reward-entry" key={c.id}>
                  <div>
                    <h3>{c.title}</h3>
                    <p>
                      {t(rewardLabels[c.kind === 'code' ? 'code' : 'broadcast'])} ·{' '}
                      {time(c.created_at)}
                    </p>
                  </div>
                  {c.kind === 'code' && (
                    <div className="reward-code">
                      <code>{c.code}</code>
                      <Button
                        type="button"
                        size="sm"
                        onClick={() => void run(() => navigator.clipboard.writeText(c.code ?? ''))}
                      >
                        {t('common.rewards.copyCode')}
                      </Button>
                      <span>{t(rewardLabels[c.enabled ? 'enabled' : 'disabled'])}</span>
                    </div>
                  )}
                  <RewardSummary items={c.rewards} assets={assets} />
                  <p className="reward-summary-text">
                    {conditionSummary(c.conditions, assets, english, t, time)}
                  </p>
                  <p>
                    {t('common.rewards.claimedCount', { amount: formatCount(c.claimed).display })}
                    {c.quota != null ? ' / ' + formatCount(c.quota).display : ''}
                    {c.kind === 'broadcast' &&
                      ' · ' +
                        t('common.rewards.deliveryCounts', {
                          pending: formatCount(c.pending).display,
                          failed: formatCount(c.failed).display,
                          skipped: formatCount(c.skipped).display,
                        })}
                  </p>
                  {c.kind === 'code' && (
                    <p>
                      {t('common.rewards.starts')}:{' '}
                      {c.starts_at != null ? time(c.starts_at) : t('common.rewards.unlimited')} ·{' '}
                      {t('common.rewards.ends')}:{' '}
                      {c.ends_at != null ? time(c.ends_at) : t('common.rewards.unlimited')}
                    </p>
                  )}
                  <div className="nb-inline">
                    {c.kind === 'code' && (
                      <>
                        <Button
                          type="button"
                          size="sm"
                          disabled={busy}
                          onClick={() => {
                            setError(undefined);
                            setEditing(c);
                          }}
                        >
                          {t('common.rewards.editLimits')}
                        </Button>
                        <Button
                          type="button"
                          size="sm"
                          disabled={busy}
                          onClick={() =>
                            void run(() => updateReward({ ...c, enabled: !c.enabled }))
                          }
                        >
                          {t(rewardLabels[c.enabled ? 'disable' : 'enable'])}
                        </Button>
                      </>
                    )}
                    {c.failed > 0 && (
                      <Button
                        type="button"
                        size="sm"
                        disabled={busy}
                        onClick={() => void run(() => retryReward(c.id))}
                      >
                        {t('common.rewards.retryFailed')}
                      </Button>
                    )}
                    <Button
                      type="button"
                      size="sm"
                      disabled={busy}
                      onClick={() => {
                        setDraft({
                          kind: c.kind,
                          title: c.title,
                          rewards: c.rewards,
                          conditions: c.conditions,
                          enabled: c.enabled,
                        });
                        setQuota(c.quota == null ? '' : String(c.quota));
                        setStart(createTimeDraft(c.starts_at ?? null));
                        setEnd(createTimeDraft(c.ends_at ?? null));
                        setCreated(undefined);
                        window.scrollTo({ top: 0, behavior: 'smooth' });
                      }}
                    >
                      {t('common.rewards.copyConfiguration')}
                    </Button>
                  </div>
                  {c.failed > 0 && (
                    <Fold title={t('common.rewards.failedRecipients')}>
                      <DeliveryFailures campaign={c.id} />
                    </Fold>
                  )}
                </article>
              ))}
            </div>
          )}
          {(pages.length > 0 || list.data?.next_cursor) && (
            <CursorPagination
              page={pages.length + 1}
              nextCursor={list.data?.next_cursor}
              onPrevious={() => {
                setCursor(pages.at(-1) ?? '');
                setPages(pages.slice(0, -1));
              }}
              onNext={(next) => {
                setPages([...pages, cursor]);
                setCursor(next);
              }}
            />
          )}
        </PanelBody>
      </Panel>
      <ConfirmDialog
        open={!!pending}
        title={t('common.rewards.confirmTitle')}
        description={
          pending?.input.kind === 'broadcast'
            ? t('common.rewards.confirmBroadcast', {
                amount: formatCount(pending.preview.matched).display,
                banned: formatCount(pending.preview.banned).display,
              })
            : t('common.rewards.confirmCode')
        }
        confirmLabel={t(rewardLabels[pending?.input.kind === 'broadcast' ? 'send' : 'createCode'])}
        busy={busy}
        onCancel={() => setPending(undefined)}
        onConfirm={() => void confirm()}
      >
        {pending && (
          <>
            <RewardSummary items={pending.input.rewards} assets={assets} />
            <p className="reward-summary-text">
              {conditionSummary(pending.input.conditions, assets, english, t, time)}
            </p>
          </>
        )}
        {!!error && <ErrorState error={error} />}
      </ConfirmDialog>
      {editing && (
        <LimitEditor
          campaign={editing}
          error={error}
          busy={busy}
          close={() => setEditing(undefined)}
          save={(next) =>
            void run(async () => {
              await updateReward(next);
              setEditing(undefined);
            })
          }
        />
      )}
    </div>
  );
}
function LimitEditor({
  campaign,
  error,
  busy,
  close,
  save,
}: {
  campaign: Campaign;
  error: unknown;
  busy: boolean;
  close: () => void;
  save: (c: Campaign) => void;
}) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(campaign.title);
  const [quota, setQuota] = useState(campaign.quota == null ? '' : String(campaign.quota));
  const [start, setStart] = useState(() => createTimeDraft(campaign.starts_at ?? null));
  const [end, setEnd] = useState(() => createTimeDraft(campaign.ends_at ?? null));
  return (
    <ConfirmDialog
      open
      title={t('common.rewards.editLimits')}
      description={t('common.rewards.frozenBundle')}
      confirmLabel={t('common.rewards.save')}
      onCancel={close}
      busy={busy}
      confirmDisabled={!validRewardWindow(quota, start, end) || !title.trim()}
      onConfirm={() =>
        save({
          ...campaign,
          title,
          quota: quota === '' ? undefined : Number(quota),
          starts_at: timeDraftValue(start) ?? undefined,
          ends_at: timeDraftValue(end) ?? undefined,
        })
      }
    >
      {!!error && <ErrorState error={error} />}
      {!validRewardWindow(quota, start, end) && (
        <Note tone="warn">{t('common.rewards.invalidWindow')}</Note>
      )}
      <div className="reward-form">
        <Field label={t('common.rewards.title')}>
          {(p) => <input {...p} value={title} onChange={(e) => setTitle(e.target.value)} />}
        </Field>
        <Field label={t('common.rewards.quota')} help={t('common.rewards.optionalLimit')}>
          {(p) => (
            <input
              {...p}
              inputMode="numeric"
              value={quota}
              onChange={(e) => setQuota(e.target.value)}
            />
          )}
        </Field>
        <TimeInput
          label={t('common.rewards.starts')}
          station="admin"
          draft={start}
          onChange={setStart}
        />
        <TimeInput label={t('common.rewards.ends')} station="admin" draft={end} onChange={setEnd} />
      </div>
    </ConfirmDialog>
  );
}
