import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { EmptyState, ErrorState, LoadingState, SectionHead } from '@shared/components/States';
import {
  Button,
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
        tiers: period.tiers.map(({ days, rewards }) => ({ days, rewards })),
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

function AttendanceEditor({
  period,
  siteDate,
  account,
  assets,
  assetsReady,
}: {
  period?: AttendancePeriod;
  siteDate: string;
  account: string;
  assets: Asset[];
  assetsReady: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [override, setOverride] = useState<AttendanceInput | null>(null);
  const draft = override ?? initialDraft(siteDate, period);
  const editable = !period || period.config_editable;
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
    const input =
      !editable && period
        ? { ...initialDraft(siteDate, period), title: draft.title, description: draft.description }
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
          setOpen(false);
        },
      },
    );
  };
  return (
    <>
      <Button
        type="button"
        variant={period ? 'secondary' : 'primary'}
        onClick={() => setOpen(true)}
      >
        {t(period ? 'admin.attendance.edit' : 'admin.attendance.create')}
      </Button>
      <Sheet
        open={open}
        keepMounted
        size="lg"
        title={t(period ? 'admin.attendance.edit' : 'admin.attendance.create')}
        closeLabel={t('common.close')}
        onClose={() => setOpen(false)}
        busy={busy}
      >
        <form
          className="attendance-editor"
          onSubmit={(event) => {
            event.preventDefault();
            save();
          }}
        >
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
            <Toggle
              label={t('admin.attendance.published')}
              checked={draft.published}
              disabled={busy || unknown || !editable}
              onChange={(published) => change({ ...draft, published })}
            />
          </fieldset>
          <fieldset
            className="attendance-editor__rules"
            disabled={busy || unknown || !editable || !assetsReady}
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
                    disabled={busy || unknown || !editable || !assetsReady}
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
          <FormActions>
            <Button type="submit" variant="primary" disabled={busy || !assetsReady}>
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
  const { query, pager } = useAttendancePage(true, account, enabled);
  const assets = useQuery({
    queryKey: [...rewardKeys.admin, account, 'assets'],
    queryFn: ({ signal }) => getAssets(true, signal),
    enabled,
  });
  return (
    <section className="attendance-management" aria-label={t('admin.attendance.title')}>
      <SectionHead
        title={t('admin.attendance.title')}
        actions={
          query.data && (
            <AttendanceEditor
              account={account}
              siteDate={query.data.site_date}
              assets={assets.data ?? []}
              assetsReady={!!assets.data && !assets.error}
            />
          )
        }
      />
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
          <Panel key={period.id} aria-label={period.title}>
            <PanelHead
              level={3}
              title={period.title}
              actions={
                <>
                  <StatusBadge tone={period.published ? 'ok' : 'neutral'}>
                    {t(period.published ? 'admin.attendance.published' : 'admin.attendance.draft')}
                  </StatusBadge>
                  <AttendanceEditor
                    period={period}
                    account={account}
                    siteDate={period.site_date}
                    assets={assets.data ?? []}
                    assetsReady={!!assets.data && !assets.error}
                  />
                </>
              }
            />
            <PanelBody>
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
                <div className="attendance-management__rewards">
                  {period.tiers.map((tier) => (
                    <div key={tier.days}>
                      <strong>
                        {t('admin.attendance.tierDays', { amount: formatCount(tier.days).display })}
                      </strong>
                      <RewardSummary items={tier.rewards} assets={assets.data ?? []} />
                    </div>
                  ))}
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
