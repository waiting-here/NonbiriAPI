import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { elevateAdmin } from '@shared/operations/api';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  adminCoreKeys,
  createLegalHold,
  getLegalHold,
  getLegalHolds,
  releaseLegalHold,
  type HeldObjectKind,
} from './core';
import { useRetainedOperation } from './useRetainedOperation';

const KINDS: HeldObjectKind[] = [
  'maintenance_event',
  'report_case',
  'announcement_audit',
  'donation',
  'request_log',
];

const KIND_LABEL_KEYS: Record<HeldObjectKind, string> = {
  maintenance_event: 'admin.legalHolds.objectKind.maintenanceEvent',
  report_case: 'admin.legalHolds.objectKind.reportCase',
  announcement_audit: 'admin.legalHolds.objectKind.announcementAudit',
  donation: 'admin.legalHolds.objectKind.donation',
  request_log: 'admin.legalHolds.objectKind.requestLog',
};

const STATE_LABEL_KEYS: Record<'active' | 'released' | 'expired', string> = {
  active: 'admin.legalHolds.state.active',
  released: 'admin.legalHolds.state.released',
  expired: 'admin.legalHolds.state.expired',
};

function isFinalAuthorityLoss(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'))
  );
}

export function LegalHoldPanel() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const authorityEpoch = useRef(0);
  const createElevationToken = useRef<string | null>(null);
  const releaseElevationToken = useRef<string | null>(null);
  const pager = useCursorPager();
  const [state, setState] = useState('');
  const [kind, setKind] = useState('');
  const [selected, setSelected] = useState('');
  const [createDraft, setCreateDraft] = useState({
    object_kind: 'report_case' as HeldObjectKind,
    object_ref: '',
    basis: '',
    days: '30',
    password: '',
    confirmed: false,
  });
  const [createExpiry, setCreateExpiry] = useState<number | null>(null);
  const [releaseDraft, setReleaseDraft] = useState({ reason: '', password: '', confirmed: false });
  const [confirmation, setConfirmation] = useState<'create' | 'release' | null>(null);
  const [elevating, setElevating] = useState(false);
  const [elevationError, setElevationError] = useState<unknown>(null);
  const list = useQuery({
    queryKey: adminCoreKeys.holds(state, kind, pager.cursor),
    queryFn: ({ signal }) => getLegalHolds(state, kind, pager.cursor, signal),
    retry: false,
  });
  const detail = useQuery({
    queryKey: adminCoreKeys.hold(selected),
    queryFn: ({ signal }) => getLegalHold(selected, signal),
    retry: false,
    enabled: Boolean(selected),
  });
  const reconcile = async () => {
    await Promise.all([list.refetch(), selected ? detail.refetch() : Promise.resolve()]);
  };
  const create = useRetainedOperation(
    async (
      input: { object_kind: HeldObjectKind; object_ref: string; basis: string; expires_at: number },
      key,
    ) => {
      const epoch = authorityEpoch.current;
      const token = createElevationToken.current;
      createElevationToken.current = null;
      if (!token)
        throw new ApiError(
          'elevated_required',
          t('admin.legalHolds.errors.elevationRequired'),
          403,
        );
      const hold = await createLegalHold(
        {
          object_kind: input.object_kind,
          object_ref: input.object_ref,
          basis: input.basis,
          expires_at: input.expires_at,
          confirmation: true,
        },
        key,
        token,
      );
      if (epoch !== authorityEpoch.current)
        throw new ApiError('authority_changed', t('admin.legalHolds.errors.authorityChanged'), 401);
      return hold;
    },
    reconcile,
  );
  const release = useRetainedOperation(
    async (input: { id: string; revision: string; reason: string }, key) => {
      const epoch = authorityEpoch.current;
      const token = releaseElevationToken.current;
      releaseElevationToken.current = null;
      if (!token)
        throw new ApiError(
          'elevated_required',
          t('admin.legalHolds.errors.elevationRequired'),
          403,
        );
      const hold = await releaseLegalHold(
        input.id,
        { expected_revision: input.revision, reason: input.reason, confirmation: true },
        key,
        token,
      );
      if (epoch !== authorityEpoch.current)
        throw new ApiError('authority_changed', t('admin.legalHolds.errors.authorityChanged'), 401);
      return hold;
    },
    reconcile,
  );
  const createError = create.error;
  const resetCreate = create.reset;
  const releaseError = release.error;
  const resetRelease = release.reset;

  /* eslint-disable react-hooks/set-state-in-effect -- External authority failures must purge fresh-elevation secrets and irreversible confirmations. */
  useEffect(() => {
    const error = list.error ?? detail.error ?? createError ?? releaseError;
    // Fresh-elevation passwords and irreversible confirmations must be erased on authority loss.
    if (isFinalAuthorityLoss(error)) {
      authorityEpoch.current += 1;
      createElevationToken.current = null;
      releaseElevationToken.current = null;
      clearStationSession(client, 'admin');
      setSelected('');
      setCreateDraft({
        object_kind: 'report_case',
        object_ref: '',
        basis: '',
        days: '30',
        password: '',
        confirmed: false,
      });
      setCreateExpiry(null);
      setReleaseDraft({ reason: '', password: '', confirmed: false });
      setConfirmation(null);
      setElevationError(null);
      resetCreate();
      resetRelease();
    }
  }, [client, createError, detail.error, list.error, releaseError, resetCreate, resetRelease]);

  useEffect(() => {
    if (!selected || !isNotFoundError(detail.error)) return;
    setSelected('');
    setReleaseDraft({ reason: '', password: '', confirmed: false });
    setConfirmation(null);
  }, [detail.error, selected]);
  /* eslint-enable react-hooks/set-state-in-effect */

  useEffect(
    () => () => {
      authorityEpoch.current += 1;
      createElevationToken.current = null;
      releaseElevationToken.current = null;
    },
    [],
  );

  const submitCreate = async () => {
    if (createExpiry === null || !createDraft.password) return;
    const epoch = authorityEpoch.current;
    const password = createDraft.password;
    const input = {
      object_kind: createDraft.object_kind,
      object_ref: createDraft.object_ref.trim(),
      basis: createDraft.basis.trim(),
      expires_at: createExpiry,
    };
    setConfirmation(null);
    setCreateDraft((current) => ({ ...current, password: '', confirmed: false }));
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(password);
      if (epoch !== authorityEpoch.current) return;
      createElevationToken.current = elevation.token;
      create.mutate(input, {
        onSuccess: (hold) => {
          setSelected(hold.id);
          setCreateExpiry(null);
          setCreateDraft({
            object_kind: 'report_case',
            object_ref: '',
            basis: '',
            days: '30',
            password: '',
            confirmed: false,
          });
        },
      });
    } catch (error) {
      if (epoch === authorityEpoch.current) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const submitRelease = async () => {
    if (!detail.data || !releaseDraft.password) return;
    const epoch = authorityEpoch.current;
    const password = releaseDraft.password;
    const input = {
      id: detail.data.id,
      revision: detail.data.revision,
      reason: releaseDraft.reason.trim(),
    };
    setConfirmation(null);
    setReleaseDraft((current) => ({ ...current, password: '', confirmed: false }));
    setElevationError(null);
    setElevating(true);
    try {
      const elevation = await elevateAdmin(password);
      if (epoch !== authorityEpoch.current) return;
      releaseElevationToken.current = elevation.token;
      release.mutate(input, {
        onSuccess: () => setReleaseDraft({ reason: '', password: '', confirmed: false }),
      });
    } catch (error) {
      if (epoch === authorityEpoch.current) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const days = Number(createDraft.days);
  const validDays = Number.isSafeInteger(days) && days >= 1 && days <= 365;
  return (
    <div className="ops-stack">
      <Card className="ops-danger">
        <h2>{t('admin.legalHolds.title')}</h2>
        <p>{t('admin.legalHolds.description')}</p>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.legalHolds.filters.state')}</span>
            <select
              value={state}
              onChange={(event) => {
                pager.reset();
                setState(event.target.value);
              }}
            >
              <option value="">{t('admin.legalHolds.filters.all')}</option>
              <option value="active">{t(STATE_LABEL_KEYS.active)}</option>
              <option value="released">{t(STATE_LABEL_KEYS.released)}</option>
              <option value="expired">{t(STATE_LABEL_KEYS.expired)}</option>
            </select>
          </label>
          <label>
            <span>{t('admin.legalHolds.filters.objectKind')}</span>
            <select
              value={kind}
              onChange={(event) => {
                pager.reset();
                setKind(event.target.value);
              }}
            >
              <option value="">{t('admin.legalHolds.filters.all')}</option>
              {KINDS.map((value) => (
                <option key={value} value={value}>
                  {t(KIND_LABEL_KEYS[value])}
                </option>
              ))}
            </select>
          </label>
        </div>
        {list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : list.data.data.length === 0 ? (
          <EmptyState
            title={t('admin.legalHolds.empty.title')}
            body={t('admin.legalHolds.empty.body')}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t('admin.legalHolds.table.object')}</th>
                    <th>{t('admin.legalHolds.table.state')}</th>
                    <th>{t('admin.legalHolds.table.window')}</th>
                    <th>{t('admin.legalHolds.table.detail')}</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.data.map((hold) => (
                    <tr key={hold.id}>
                      <td>
                        {t(KIND_LABEL_KEYS[hold.object_kind])}
                        <small>{hold.object_ref}</small>
                      </td>
                      <td>
                        <StatusBadge
                          active={hold.state === 'active'}
                          label={t(STATE_LABEL_KEYS[hold.state])}
                        />
                      </td>
                      <td>
                        {formatDateTime(hold.created_at)} — {formatDateTime(hold.expires_at)}
                      </td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(hold.id)}
                        >
                          {t('admin.legalHolds.actions.metadata')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={list.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
      {selected ? (
        <Card>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : (
            <>
              <h3>{t('admin.legalHolds.detail.title')}</h3>
              <dl className="ops-kv">
                <dt>{t('admin.legalHolds.detail.idRevision')}</dt>
                <dd>
                  {detail.data.id} / {detail.data.revision}
                </dd>
                <dt>{t('admin.legalHolds.detail.object')}</dt>
                <dd>
                  {t(KIND_LABEL_KEYS[detail.data.object_kind])} · {detail.data.object_ref}
                </dd>
                <dt>{t('admin.legalHolds.detail.state')}</dt>
                <dd>{t(STATE_LABEL_KEYS[detail.data.state])}</dd>
                <dt>{t('admin.legalHolds.detail.basis')}</dt>
                <dd>{detail.data.basis}</dd>
                <dt>{t('admin.legalHolds.detail.expires')}</dt>
                <dd>{formatDateTime(detail.data.expires_at)}</dd>
                {detail.data.end_reason ? (
                  <>
                    <dt>{t('admin.legalHolds.detail.endReason')}</dt>
                    <dd>
                      {detail.data.state === 'expired'
                        ? t('admin.legalHolds.endReason.expired')
                        : t('admin.legalHolds.endReason.released', {
                            reason: detail.data.end_reason,
                          })}
                    </dd>
                  </>
                ) : null}
              </dl>
              {detail.data.state === 'active' ? (
                <>
                  <label className="ops-form-field">
                    <span>{t('admin.legalHolds.release.reason')}</span>
                    <input
                      maxLength={1024}
                      value={releaseDraft.reason}
                      onChange={(event) =>
                        setReleaseDraft({ ...releaseDraft, reason: event.target.value })
                      }
                    />
                  </label>
                  <label className="ops-form-field">
                    <span>{t('admin.legalHolds.release.password')}</span>
                    <input
                      type="password"
                      autoComplete="current-password"
                      value={releaseDraft.password}
                      onChange={(event) =>
                        setReleaseDraft({ ...releaseDraft, password: event.target.value })
                      }
                    />
                  </label>
                  <label className="checkbox-label">
                    <input
                      type="checkbox"
                      checked={releaseDraft.confirmed}
                      onChange={(event) =>
                        setReleaseDraft({ ...releaseDraft, confirmed: event.target.checked })
                      }
                    />
                    <span>{t('admin.legalHolds.release.confirmation')}</span>
                  </label>
                  <button
                    className="btn btn-danger"
                    type="button"
                    disabled={
                      !releaseDraft.reason.trim() ||
                      !releaseDraft.password ||
                      !releaseDraft.confirmed ||
                      release.isPending
                    }
                    onClick={() => setConfirmation('release')}
                  >
                    {t('admin.legalHolds.actions.release')}
                  </button>
                </>
              ) : null}
            </>
          )}
        </Card>
      ) : null}
      <Card className="ops-danger">
        <h3>{t('admin.legalHolds.create.title')}</h3>
        <div className="ops-field-grid">
          <label>
            <span>{t('admin.legalHolds.create.objectKind')}</span>
            <select
              value={createDraft.object_kind}
              onChange={(event) => {
                setCreateExpiry(null);
                setCreateDraft({
                  ...createDraft,
                  object_kind: event.target.value as HeldObjectKind,
                });
              }}
            >
              {KINDS.map((value) => (
                <option key={value} value={value}>
                  {t(KIND_LABEL_KEYS[value])}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>{t('admin.legalHolds.create.objectReference')}</span>
            <input
              value={createDraft.object_ref}
              onChange={(event) => {
                setCreateExpiry(null);
                setCreateDraft({ ...createDraft, object_ref: event.target.value });
              }}
            />
          </label>
          <label>
            <span>{t('admin.legalHolds.create.days')}</span>
            <input
              type="number"
              min="1"
              max="365"
              value={createDraft.days}
              onChange={(event) => {
                setCreateExpiry(null);
                setCreateDraft({ ...createDraft, days: event.target.value });
              }}
            />
          </label>
          <label>
            <span>{t('admin.legalHolds.create.password')}</span>
            <input
              type="password"
              autoComplete="current-password"
              value={createDraft.password}
              onChange={(event) => setCreateDraft({ ...createDraft, password: event.target.value })}
            />
          </label>
        </div>
        <label className="ops-form-field">
          <span>{t('admin.legalHolds.create.basis')}</span>
          <textarea
            maxLength={1024}
            value={createDraft.basis}
            onChange={(event) => {
              setCreateExpiry(null);
              setCreateDraft({ ...createDraft, basis: event.target.value });
            }}
          />
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={createDraft.confirmed}
            onChange={(event) =>
              setCreateDraft({ ...createDraft, confirmed: event.target.checked })
            }
          />
          <span>{t('admin.legalHolds.create.confirmation')}</span>
        </label>
        {create.error ? (
          <ErrorState error={create.error} />
        ) : release.error ? (
          <ErrorState error={release.error} />
        ) : elevationError ? (
          <ErrorState error={elevationError} />
        ) : null}
        <button
          className="btn btn-danger"
          type="button"
          disabled={
            !createDraft.object_ref.trim() ||
            !createDraft.basis.trim() ||
            !createDraft.password ||
            !createDraft.confirmed ||
            !validDays ||
            create.isPending ||
            elevating
          }
          onClick={() => {
            setCreateExpiry((value) => value ?? Math.floor(Date.now() / 1_000) + days * 86_400);
            setConfirmation('create');
          }}
        >
          {t('admin.legalHolds.actions.create')}
        </button>
      </Card>
      {confirmation ? (
        <ConfirmDialog
          open
          title={t(
            confirmation === 'create'
              ? 'admin.legalHolds.confirm.createTitle'
              : 'admin.legalHolds.confirm.releaseTitle',
          )}
          description={
            confirmation === 'create'
              ? t('admin.legalHolds.confirm.createDescription', { days })
              : t('admin.legalHolds.confirm.releaseDescription')
          }
          confirmLabel={t(
            confirmation === 'create'
              ? 'admin.legalHolds.confirm.createAction'
              : 'admin.legalHolds.confirm.releaseAction',
          )}
          danger
          busy={create.isPending || release.isPending || elevating}
          onCancel={() => {
            setConfirmation(null);
            setCreateDraft((current) => ({ ...current, password: '', confirmed: false }));
            setReleaseDraft((current) => ({ ...current, password: '', confirmed: false }));
            setElevationError(null);
          }}
          onConfirm={() => {
            if (confirmation === 'create') void submitCreate();
            else void submitRelease();
          }}
        />
      ) : null}
    </div>
  );
}
