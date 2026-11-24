import { DraftLeaveGuard } from '@shared/components/charity/DraftLeaveScope';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { EmptyState, ErrorState, LoadingState, SectionHead } from '@shared/components/States';
import {
  Button,
  ChoiceRow,
  Field,
  Fields,
  FormActions,
  Note,
  OutcomeNote,
  Panel,
  PanelBody,
  PanelHead,
  Segmented,
  Sheet,
  StatusBadge,
  Toggle,
} from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { assetName } from '@shared/rewards/copy';
import { ExactCredits } from '../../../user/features/core/components';
import { RewardEditor } from '@shared/rewards/RewardEditor';
import { RewardSummary } from '@shared/rewards/RewardSummary';
import { getAssets, rewardKeys, type Asset } from '@shared/rewards/api';
import { MarkdownText } from '@shared/components/MarkdownText';
import { formatOffset } from '@shared/time';
import { formatCount } from '@shared/utils/formatNumber';
import {
  attendanceKeys,
  attendanceMetadata,
  saveAttendance,
  type AttendanceInput,
  type AttendancePeriod,
} from '@shared/attendance/api';
import { useAttendancePage } from '@shared/attendance/queries';
import { CampaignCover } from '@shared/attendance/CampaignCover';
import { MilestoneHeading } from '@shared/attendance/MilestoneHeading';
import { AttendanceCoverEditor } from './AttendanceCoverEditor';
import '@shared/rewards/rewards.css';
import './attendance-management.css';

function addDays(date: string, days: number): string {
  const instant = new Date(date + 'T00:00:00Z');
  instant.setUTCDate(instant.getUTCDate() + days);
  return instant.toISOString().slice(0, 10);
}
function initialDraft(siteDate: string, period?: AttendancePeriod): AttendanceInput {
  return period
    ? {
        title: period.title,
        description: period.description,
        start_date: period.start_date,
        end_date: period.end_date,
        mode: period.mode,
        published: period.published,
        cover: period.cover,
        tiers: period.tiers.map(({ days, rewards, featured }) => ({ days, rewards, featured })),
      }
    : {
        title: '',
        description: '',
        start_date: addDays(siteDate, 1),
        end_date: addDays(siteDate, 7),
        mode: 'cumulative',
        published: true,
        tiers: [{ days: 7, rewards: [{ asset: 'general', quantity: '100' }] }],
      };
}

function keepStartedRules(saved: AttendanceInput, pending: AttendanceInput): AttendanceInput {
  const featured = new Map(pending.tiers.map((tier) => [tier.days, tier.featured]));
  return {
    ...saved,
    title: pending.title,
    description: pending.description,
    cover: pending.cover,
    tiers: saved.tiers.map((tier) => ({
      ...tier,
      featured: featured.get(tier.days) ?? tier.featured,
    })),
  };
}

function AttendanceEditor({
  period,
  copy,
  close,
  open,
  siteDate,
  account,
  assets,
  assetsReady,
}: {
  period?: AttendancePeriod;
  copy?: AttendancePeriod;
  close: (retain?: boolean) => void;
  open: boolean;
  siteDate: string;
  account: string;
  assets: Asset[];
  assetsReady: boolean;
}) {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const [discard, setDiscard] = useState(false);
  const [base, setBase] = useState(period);
  const [override, setOverride] = useState<AttendanceInput | null>(null);
  const editable = !period || period.config_editable;
  const saved = initialDraft(siteDate, base);
  if (copy) {
    const origin = initialDraft(siteDate, copy);
    Object.assign(saved, origin, {
      published: false,
      start_date: saved.start_date,
      end_date: addDays(
        saved.start_date,
        Math.round((Date.parse(origin.end_date) - Date.parse(origin.start_date)) / 86400000),
      ),
    });
  }
  const stale = base?.revision !== period?.revision;
  if (stale && !override) setBase(period);
  const draft = override ? (editable ? override : keepStartedRules(saved, override)) : saved;
  const dirty = JSON.stringify(saved) !== JSON.stringify(draft);
  const duplicate = new Set(draft.tiers.map((tier) => tier.days)).size !== draft.tiers.length;
  const root = attendanceKeys.root(true, account);
  const operation = useRetainedOperation(
    (
      input: { draft: AttendanceInput; period?: Pick<AttendancePeriod, 'id' | 'revision'> },
      key,
      context,
    ) => saveAttendance(input.draft, key, input.period, context.signal),
    async () => {
      await client.invalidateQueries({ queryKey: root });
    },
    root,
  );
  const unknown = operation.outcome === 'unknown';
  const busy = operation.isPending;
  const change = (next: AttendanceInput) => {
    operation.reset();
    setOverride(next);
  };
  const save = () => {
    if (!unknown && (stale || duplicate)) return;
    const cover = draft.cover;
    const input =
      cover?.kind === 'external'
        ? { ...draft, cover: { ...cover, value: cover.value.trim() } }
        : draft;
    operation.mutate(
      unknown && operation.variables
        ? operation.variables
        : {
            draft: input,
            period: period ? { id: period.id, revision: period.revision } : undefined,
          },
      {
        onSuccess: () => {
          setOverride(null);
          close();
        },
      },
    );
  };
  return (
    <>
      <DraftLeaveGuard dirty={dirty} />
      {discard && (
        <ConfirmDialog
          open
          danger
          title={t('admin.audit.discardEdits')}
          description={t('admin.audit.discardBody')}
          confirmLabel={t('admin.games.discardGameEdits', { game: '' })}
          onCancel={() => setDiscard(false)}
          onConfirm={() => close()}
        />
      )}
      <Sheet
        open={open}
        size="lg"
        title={t(period ? 'admin.attendance.edit' : 'admin.attendance.create')}
        closeLabel={t('common.close')}
        onClose={() => {
          if (unknown) close(true);
          else if (dirty) setDiscard(true);
          else close();
        }}
        busy={busy}
      >
        <form
          className="attendance-editor"
          onSubmit={(event) => {
            event.preventDefault();
            save();
          }}
        >
          {duplicate && <Note tone="bad">{t('admin.audit.duplicateDays')}</Note>}
          {stale && (
            <Note tone="warn">
              {t('admin.games.validation.changedElsewhere')}{' '}
              <Button
                type="button"
                onClick={() => {
                  setBase(period);
                  setOverride(null);
                }}
              >
                {t('admin.games.restoreAuthorityValues')}
              </Button>
            </Note>
          )}
          {operation.error && !unknown ? <ErrorState error={operation.error} /> : null}
          {unknown && (
            <OutcomeNote outcome={{ kind: 'unknown' }} body={t('admin.attendance.retryHelp')} />
          )}
          {operation.outcome === 'conflict' && <OutcomeNote outcome={{ kind: 'conflict' }} />}
          <Fields columns={1}>
            <Field label={t('admin.attendance.name')}>
              {(props) => (
                <input
                  {...props}
                  required
                  maxLength={80}
                  disabled={busy || unknown}
                  value={draft.title}
                  onChange={(event) => change({ ...draft, title: event.target.value })}
                />
              )}
            </Field>
            <Field label={t('admin.attendance.description')} help={t('admin.attendance.markdown')}>
              {(props) => (
                <textarea
                  {...props}
                  rows={4}
                  maxLength={2000}
                  disabled={busy || unknown}
                  value={draft.description}
                  onChange={(event) => change({ ...draft, description: event.target.value })}
                />
              )}
            </Field>
          </Fields>
          {!editable && <Note>{t('admin.attendance.locked')}</Note>}
          <fieldset className="attendance-editor__rules" disabled={busy || unknown}>
            <legend>{t('admin.attendance.featuredRewards')}</legend>
            <div className="attendance-editor__featured">
              {draft.tiers.map((tier, index) => (
                <ChoiceRow
                  key={index}
                  label={
                    tier.days > 0
                      ? t('admin.attendance.tierDays', { amount: formatCount(tier.days).display })
                      : t('admin.attendance.untimedMilestone', {
                          amount: formatCount(index + 1).display,
                        })
                  }
                  description={tier.rewards.map((reward, rewardIndex) => (
                    <span key={rewardIndex} className="attendance-editor__featured-item">
                      {assetName(assets, reward.asset, i18n.language.startsWith('en'))}
                      {reward.quantity && (
                        <>
                          {' '}
                          × <ExactCredits value={reward.quantity} />
                        </>
                      )}
                    </span>
                  ))}
                  checked={Boolean(tier.featured)}
                  disabled={busy || unknown}
                  onChange={(featured) =>
                    change({
                      ...draft,
                      tiers: draft.tiers.map((entry, i) =>
                        i === index ? { ...entry, featured } : entry,
                      ),
                    })
                  }
                />
              ))}
            </div>
          </fieldset>
          <AttendanceCoverEditor
            cover={draft.cover}
            savedCover={period?.cover}
            disabled={busy || unknown}
            onChange={(cover) => change({ ...draft, cover })}
          />
          <fieldset className="attendance-editor__rules" disabled={busy || unknown || !editable}>
            <legend>{t('admin.attendance.schedule')}</legend>
            <p className="nb-small nb-muted">{t('admin.attendance.dateHelp')}</p>
            <Fields columns={2}>
              <Field label={t('admin.attendance.start')}>
                {(props) => (
                  <input
                    {...props}
                    required
                    type="date"
                    min={editable ? addDays(siteDate, 1) : undefined}
                    value={draft.start_date}
                    onChange={(event) => change({ ...draft, start_date: event.target.value })}
                  />
                )}
              </Field>
              <Field label={t('admin.attendance.end')}>
                {(props) => (
                  <input
                    {...props}
                    required
                    type="date"
                    min={draft.start_date}
                    value={draft.end_date}
                    onChange={(event) => change({ ...draft, end_date: event.target.value })}
                  />
                )}
              </Field>
            </Fields>
            <div className="attendance-editor__mode">
              <Segmented
                label={t('admin.attendance.mode')}
                value={draft.mode}
                disabled={busy || unknown || !editable}
                options={[
                  { value: 'cumulative', label: t('admin.attendance.cumulative') },
                  { value: 'consecutive', label: t('admin.attendance.consecutive') },
                ]}
                onChange={(mode) => change({ ...draft, mode })}
              />
              <p className="nb-small nb-muted">
                {t(
                  draft.mode === 'consecutive'
                    ? 'admin.attendance.consecutiveHelp'
                    : 'admin.attendance.cumulativeHelp',
                )}
              </p>
            </div>
            <Toggle
              label={t('admin.attendance.published')}
              checked={draft.published}
              disabled={busy || unknown || !editable}
              onChange={(published) => change({ ...draft, published })}
            />
          </fieldset>
          {editable && (
            <fieldset
              className="attendance-editor__rules"
              disabled={busy || unknown || !assetsReady}
            >
              <legend>{t('admin.attendance.rewards')}</legend>
              <p className="nb-small nb-muted">{t('admin.attendance.rewardHelp')}</p>
              <div className="attendance-editor__tiers">
                {draft.tiers.map((tier, index) => (
                  <div className="attendance-editor__tier" key={index}>
                    <div className="attendance-editor__tier-head">
                      <Field size="short" label={t('admin.attendance.days')}>
                        {(props) => (
                          <input
                            {...props}
                            required
                            type="number"
                            min={1}
                            max={3660}
                            step={1}
                            value={tier.days || ''}
                            onChange={(event) =>
                              change({
                                ...draft,
                                tiers: draft.tiers.map((entry, i) =>
                                  i === index
                                    ? { ...entry, days: Number(event.target.value) }
                                    : entry,
                                ),
                              })
                            }
                          />
                        )}
                      </Field>
                      <Button
                        type="button"
                        size="sm"
                        disabled={busy || unknown || !editable || draft.tiers.length === 1}
                        onClick={() =>
                          change({ ...draft, tiers: draft.tiers.filter((_, i) => i !== index) })
                        }
                      >
                        {t('admin.attendance.removeTier')}
                      </Button>
                    </div>
                    <RewardEditor
                      assets={assets}
                      value={tier.rewards}
                      disabled={busy || unknown || !assetsReady}
                      onChange={(rewards) =>
                        change({
                          ...draft,
                          tiers: draft.tiers.map((entry, i) =>
                            i === index ? { ...entry, rewards } : entry,
                          ),
                        })
                      }
                    />
                  </div>
                ))}
                <div>
                  <Button
                    type="button"
                    disabled={
                      busy || unknown || !editable || !assetsReady || draft.tiers.length >= 366
                    }
                    onClick={() =>
                      change({
                        ...draft,
                        tiers: [
                          ...draft.tiers,
                          {
                            days: (draft.tiers.at(-1)?.days ?? 0) + 7,
                            rewards: [{ asset: 'general', quantity: '100' }],
                          },
                        ],
                      })
                    }
                  >
                    {t('admin.attendance.addTier')}
                  </Button>
                </div>
              </div>
            </fieldset>
          )}
          <FormActions>
            <Button
              type="submit"
              variant="primary"
              disabled={busy || (!unknown && (stale || duplicate || (editable && !assetsReady)))}
            >
              {t(
                busy
                  ? 'admin.attendance.saving'
                  : unknown
                    ? 'common.retry'
                    : 'admin.attendance.save',
              )}
            </Button>
          </FormActions>
        </form>
      </Sheet>
    </>
  );
}

export function AttendanceManagement({ account, enabled }: { account: string; enabled: boolean }) {
  const { t } = useTranslation();
  const [editor, setEditor] = useState<{
    mode: 'new' | 'copy' | 'edit';
    source?: AttendancePeriod;
  } | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const pendingEditor = editorOpen ? null : editor;
  const blockedEditor = (mode: NonNullable<typeof editor>['mode'], id?: string) =>
    Boolean(pendingEditor && (pendingEditor.mode !== mode || pendingEditor.source?.id !== id));
  const openEditor = (next: NonNullable<typeof editor>) => {
    if (blockedEditor(next.mode, next.source?.id)) return;
    if (!pendingEditor) setEditor(next);
    setEditorOpen(true);
  };
  const { query, pager } = useAttendancePage(true, account, enabled);
  const assets = useQuery({
    queryKey: [...rewardKeys.admin, account, 'assets'],
    queryFn: ({ signal }) => getAssets(true, signal),
    enabled,
  });
  return (
    <section
      id="attendance"
      className="attendance-management"
      aria-label={t('admin.attendance.title')}
    >
      <SectionHead
        title={t('admin.attendance.title')}
        actions={
          query.data && (
            <Button
              type="button"
              variant="primary"
              disabled={blockedEditor('new')}
              onClick={() => openEditor({ mode: 'new' })}
            >
              {t('admin.attendance.create')}
            </Button>
          )
        }
      />
      {pendingEditor && (
        <div className="nb-cluster">
          <span>
            {t('admin.audit.attendancePending', {
              title: pendingEditor.source?.title ?? t('admin.attendance.create'),
            })}
          </span>
          <Button type="button" onClick={() => setEditorOpen(true)}>
            {t('admin.audit.reopenAttendance')}
          </Button>
        </div>
      )}
      {editor && query.data && (
        <AttendanceEditor
          key={editor.mode + (editor.source?.id ?? '')}
          account={account}
          siteDate={query.data.site_date}
          period={
            editor.mode === 'edit'
              ? (query.data.items.find((item) => item.id === editor.source?.id) ?? editor.source)
              : undefined
          }
          copy={editor.mode === 'copy' ? editor.source : undefined}
          open={editorOpen}
          close={(retain) => {
            setEditorOpen(false);
            if (!retain) setEditor(null);
          }}
          assets={assets.data ?? []}
          assetsReady={!!assets.data && !assets.error}
        />
      )}
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {assets.error && <ErrorState error={assets.error} onRetry={() => void assets.refetch()} />}
      {query.data && (
        <p className="nb-small nb-muted">
          {t('admin.attendance.siteDate', {
            date: query.data.site_date,
            zone: formatOffset(query.data.timezone_offset_minutes * 60),
          })}
        </p>
      )}
      {query.data?.items.length === 0 && (
        <EmptyState title={t('admin.attendance.empty')} body={t('admin.attendance.emptyBody')} />
      )}
      <div className="attendance-management__list">
        {query.data?.items.map((period) => (
          <Panel key={period.id} aria-label={period.title} className="attendance-management__card">
            <PanelHead
              level={3}
              title={period.title}
              actions={
                <>
                  <StatusBadge
                    tone={
                      period.status === 'active'
                        ? 'ok'
                        : period.status === 'upcoming'
                          ? 'info'
                          : 'neutral'
                    }
                  >
                    {t(
                      period.status === 'active'
                        ? 'admin.attendance.statusActive'
                        : period.status === 'upcoming'
                          ? 'admin.attendance.statusUpcoming'
                          : 'admin.attendance.statusEnded',
                    )}
                  </StatusBadge>
                  <StatusBadge tone={period.published ? 'ok' : 'neutral'}>
                    {t(period.published ? 'admin.attendance.published' : 'admin.attendance.draft')}
                  </StatusBadge>
                  <Button
                    type="button"
                    size="sm"
                    disabled={blockedEditor('edit', period.id)}
                    onClick={() => openEditor({ mode: 'edit', source: period })}
                  >
                    {t('admin.attendance.edit')}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    disabled={blockedEditor('copy', period.id)}
                    onClick={() => openEditor({ mode: 'copy', source: period })}
                  >
                    {t('admin.audit.copyAttendance')}
                  </Button>
                </>
              }
            />
            <PanelBody>
              <div className="attendance-management__body">
                <CampaignCover cover={period.cover} className="attendance-management__cover" />
                <div className="attendance-management__summary">
                  <p>
                    <time dateTime={period.start_date}>{period.start_date}</time> —{' '}
                    <time dateTime={period.end_date}>{period.end_date}</time> ·{' '}
                    {t(
                      period.mode === 'consecutive'
                        ? 'admin.attendance.consecutive'
                        : 'admin.attendance.cumulative',
                    )}
                  </p>
                  {period.description && <MarkdownText>{period.description}</MarkdownText>}
                  <ul className="attendance-management__rewards">
                    {period.tiers.map((tier) => (
                      <li
                        className={
                          tier.featured
                            ? 'attendance-management__tier attendance-milestone--featured'
                            : 'attendance-management__tier'
                        }
                        key={tier.days}
                      >
                        <MilestoneHeading days={tier.days} featured={tier.featured} />
                        <RewardSummary items={tier.rewards} assets={assets.data ?? []} />
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            </PanelBody>
          </Panel>
        ))}
      </div>
      {query.data && (
        <PagePagination
          metadata={attendanceMetadata(query.data)}
          requestedPage={pager.page}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
          busy={query.isFetching}
        />
      )}
    </section>
  );
}
