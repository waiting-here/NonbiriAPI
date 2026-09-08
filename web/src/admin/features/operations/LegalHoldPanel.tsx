import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
  type StationSessionSnapshot,
} from '@shared/charityManagement';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { elevateAdmin } from '@shared/operations/api';
import { ApiError, isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useAdminSession } from '../../data';
import { createLegalHold, releaseLegalHold, type HeldObjectKind } from './core';
import {
  isLegalHoldID,
  type LegalHoldKindFilter,
  type LegalHoldStateFilter,
  useLegalHoldDetail,
  useLegalHoldPage,
} from './legalHoldPages';
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

const HOLD_LIST_TYPE = 'legal-holds';
const STATE_PARAM = 'hold_state';
const KIND_PARAM = 'hold_kind';
const ID_PARAM = 'hold_id';
const PAGE_PARAM = 'hold_page';
const PAGE_SIZE_PARAM = 'hold_page_size';
const STATES: readonly LegalHoldStateFilter[] = ['', 'active', 'released', 'expired'];
const FILTER_KINDS: readonly LegalHoldKindFilter[] = ['', ...KINDS];

function singleSearchValue(searchParams: URLSearchParams, name: string): string | undefined {
  const values = searchParams.getAll(name);
  return values.length === 1 ? values[0] : undefined;
}

function readFilter<T extends string>(
  searchParams: URLSearchParams,
  name: string,
  allowed: readonly T[],
): T {
  const value = singleSearchValue(searchParams, name);
  return value !== undefined && allowed.includes(value as T) ? (value as T) : allowed[0];
}

function filterNeedsNormalization<T extends string>(
  searchParams: URLSearchParams,
  name: string,
  allowed: readonly T[],
): boolean {
  const values = searchParams.getAll(name);
  return values.length > 0 && (values.length !== 1 || !allowed.includes(values[0] as T));
}

function selectedHoldID(searchParams: URLSearchParams): string {
  const value = singleSearchValue(searchParams, ID_PARAM);
  return value && isLegalHoldID(value) ? value : '';
}

function isFinalAuthorityLoss(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'))
  );
}

function isInvalidResponse(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'invalid_response';
}

export function LegalHoldPanel() {
  const session = useAdminSession();
  const [closedError, setClosedError] = useState<unknown>(null);
  const [, setSearchParams] = useSearchParams();
  const account = session.data?.admin.username;
  const [observedAccount, setObservedAccount] = useState(account);
  const transitioning = observedAccount !== undefined && observedAccount !== account;
  /* eslint-disable react-hooks/set-state-in-effect -- Reset deep links when the authoritative account changes, before mounting its private panel. */
  useEffect(() => {
    if (observedAccount === account) return;
    if (observedAccount !== undefined) setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.delete(ID_PARAM);
      next.delete(PAGE_PARAM);
      return next;
    }, { replace: true });
    setObservedAccount(account);
  }, [account, observedAccount, setSearchParams]);
  /* eslint-enable react-hooks/set-state-in-effect */
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  if (!session.data && closedError) return <ErrorState error={closedError} />;
  if (!session.data || session.isPending || transitioning) return <LoadingState />;
  return (
    <LegalHoldSessionPanel
      key={session.data.admin.username}
      session={session}
      onAuthorityLoss={setClosedError}
    />
  );
}

function LegalHoldSessionPanel({
  session,
  onAuthorityLoss,
}: {
  session: ReturnType<typeof useAdminSession>;
  onAuthorityLoss: (error: unknown) => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const authorityEpoch = useRef(0);
  const createElevationToken = useRef<string | null>(null);
  const releaseElevationToken = useRef<string | null>(null);
  const createSession = useRef<StationSessionSnapshot | null>(null);
  const releaseSession = useRef<StationSessionSnapshot | null>(null);
  const handledAuthorityError = useRef<unknown>(null);
  const [previousAccountID, setPreviousAccountID] = useState<string | undefined>(undefined);
  const [authorityError, setAuthorityError] = useState<unknown>(null);
  const state = readFilter(searchParams, STATE_PARAM, STATES);
  const kind = readFilter(searchParams, KIND_PARAM, FILTER_KINDS);
  const selected = selectedHoldID(searchParams);
  const accountID = session.data ? `admin:${session.data.admin.username}` : undefined;
  const accountTransitioning =
    previousAccountID !== undefined && accountID !== undefined && previousAccountID !== accountID;
  const scopeReady = Boolean(accountID) && !session.error;
  const filtersReady =
    !filterNeedsNormalization(searchParams, STATE_PARAM, STATES) &&
    !filterNeedsNormalization(searchParams, KIND_PARAM, FILTER_KINDS);
  const selectedParamValues = searchParams.getAll(ID_PARAM);
  const selectedParamReady =
    selectedParamValues.length === 0 ||
    (selectedParamValues.length === 1 && isLegalHoldID(selectedParamValues[0] ?? ''));
  const pager = useUrlPagePager({
    station: 'admin',
    listType: HOLD_LIST_TYPE,
    scopeKey: accountID ?? 'anonymous',
    scopeReady,
    resetKey: `${state}\u0000${kind}`,
    pageParam: PAGE_PARAM,
    pageSizeParam: PAGE_SIZE_PARAM,
  });
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
  const list = useLegalHoldPage(
    accountID,
    state,
    kind,
    pager.page,
    pager.pageSize,
    scopeReady &&
      !accountTransitioning &&
      filtersReady &&
      !session.isPending &&
      !session.isFetching,
  );
  const detail = useLegalHoldDetail(
    accountID,
    selected,
    scopeReady &&
      !accountTransitioning &&
      selectedParamReady &&
      !session.isPending &&
      !session.isFetching,
  );
  const reconcile = async () => {
    if (!session.data || !client.getQueryData(['admin', 'session'])) return;
    await Promise.all([list.refetch(), selected ? detail.refetch() : Promise.resolve()]);
  };
  const assertAuthority = (epoch: number, snapshot: StationSessionSnapshot | null) => {
    if (
      epoch !== authorityEpoch.current ||
      !snapshot ||
      !stationSessionMatches(client, 'admin', snapshot)
    )
      throw new StationSessionChangedError();
  };
  const create = useRetainedOperation(
    async (
      input: { object_kind: HeldObjectKind; object_ref: string; basis: string; expires_at: number },
      key,
    ) => {
      const epoch = authorityEpoch.current;
      const token = createElevationToken.current;
      const snapshot = createSession.current;
      createElevationToken.current = null;
      if (!token)
        throw new ApiError(
          'elevated_required',
          t('admin.legalHolds.errors.elevationRequired'),
          403,
        );
      assertAuthority(epoch, snapshot);
      try {
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
        assertAuthority(epoch, snapshot);
        return hold;
      } catch (error) {
        assertAuthority(epoch, snapshot);
        throw error;
      }
    },
    reconcile,
  );
  const release = useRetainedOperation(
    async (input: { id: string; revision: string; reason: string }, key) => {
      const epoch = authorityEpoch.current;
      const token = releaseElevationToken.current;
      const snapshot = releaseSession.current;
      releaseElevationToken.current = null;
      if (!token)
        throw new ApiError(
          'elevated_required',
          t('admin.legalHolds.errors.elevationRequired'),
          403,
        );
      assertAuthority(epoch, snapshot);
      try {
        const hold = await releaseLegalHold(
          input.id,
          { expected_revision: input.revision, reason: input.reason, confirmation: true },
          key,
          token,
        );
        assertAuthority(epoch, snapshot);
        return hold;
      } catch (error) {
        assertAuthority(epoch, snapshot);
        throw error;
      }
    },
    reconcile,
  );
  const createError = create.error;
  const resetCreate = create.reset;
  const releaseError = release.error;
  const resetRelease = release.reset;

  /* eslint-disable react-hooks/set-state-in-effect -- External authority failures must purge fresh-elevation secrets and irreversible confirmations. */
  useEffect(() => {
    const invalidState = filterNeedsNormalization(searchParams, STATE_PARAM, STATES);
    const invalidKind = filterNeedsNormalization(searchParams, KIND_PARAM, FILTER_KINDS);
    const invalidID = selectedParamValues.length > 0 && !selectedParamReady;
    if (!invalidState && !invalidKind && !invalidID) return;
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (invalidState) next.delete(STATE_PARAM);
        if (invalidKind) next.delete(KIND_PARAM);
        if (invalidID) next.delete(ID_PARAM);
        return next;
      },
      { replace: true },
    );
  }, [searchParams, selectedParamReady, selectedParamValues.length, setSearchParams]);

  useEffect(() => {
    const previous = previousAccountID;
    if (!previous && accountID && !session.error) setAuthorityError(null);
    if (previous && accountID && previous !== accountID) {
      authorityEpoch.current += 1;
      createElevationToken.current = null;
      releaseElevationToken.current = null;
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          next.delete(ID_PARAM);
          return next;
        },
        { replace: true },
      );
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
      setAuthorityError(null);
      resetCreate();
      resetRelease();
    }
    setPreviousAccountID(accountID);
  }, [accountID, previousAccountID, resetCreate, resetRelease, session.error, setSearchParams]);

  useEffect(() => {
    const error = [
      session.error,
      list.error,
      detail.error,
      createError,
      releaseError,
      elevationError,
    ].find(isFinalAuthorityLoss);
    // Fresh-elevation passwords and irreversible confirmations must be erased on authority loss.
    if (!isFinalAuthorityLoss(error)) {
      if (!error) handledAuthorityError.current = null;
      return;
    }
    if (handledAuthorityError.current === error) return;
    handledAuthorityError.current = error;
    authorityEpoch.current += 1;
    createElevationToken.current = null;
    releaseElevationToken.current = null;
    onAuthorityLoss(error);
    clearStationSession(client, 'admin');
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete(ID_PARAM);
        return next;
      },
      { replace: true },
    );
    setAuthorityError(error);
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
  }, [
    client,
    createError,
    detail.error,
    elevationError,
    list.error,
    onAuthorityLoss,
    releaseError,
    resetCreate,
    resetRelease,
    session.error,
    setSearchParams,
  ]);

  useEffect(() => {
    if (!selected || (!isNotFoundError(detail.error) && !isInvalidResponse(detail.error))) return;
    setSearchParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete(ID_PARAM);
        return next;
      },
      { replace: true },
    );
    setReleaseDraft({ reason: '', password: '', confirmed: false });
    setConfirmation(null);
  }, [detail.error, selected, setSearchParams]);
  useEffect(() => {
    setReleaseDraft({ reason: '', password: '', confirmed: false });
    setConfirmation(null);
    releaseElevationToken.current = null;
    resetRelease();
  }, [selected, resetRelease]);
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
    if (
      createExpiry === null ||
      !createDraft.password ||
      elevating ||
      create.isPending ||
      release.isPending ||
      session.isFetching ||
      list.error
    )
      return;
    const epoch = authorityEpoch.current;
    const snapshot = captureStationSession(client, 'admin');
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
      assertAuthority(epoch, snapshot);
      createElevationToken.current = elevation.token;
      createSession.current = snapshot;
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
      if (epoch === authorityEpoch.current && stationSessionMatches(client, 'admin', snapshot)) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const submitRelease = async () => {
    if (
      !detail.data ||
      !releaseDraft.password ||
      elevating ||
      create.isPending ||
      release.isPending ||
      session.isFetching ||
      detail.isFetching ||
      detail.error ||
      list.error
    )
      return;
    const epoch = authorityEpoch.current;
    const snapshot = captureStationSession(client, 'admin');
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
      assertAuthority(epoch, snapshot);
      releaseElevationToken.current = elevation.token;
      releaseSession.current = snapshot;
      release.mutate(input, {
        onSuccess: () => setReleaseDraft({ reason: '', password: '', confirmed: false }),
      });
    } catch (error) {
      if (epoch === authorityEpoch.current && stationSessionMatches(client, 'admin', snapshot)) setElevationError(error);
    } finally {
      if (epoch === authorityEpoch.current) setElevating(false);
    }
  };

  const days = Number(createDraft.days);
  const validDays = Number.isSafeInteger(days) && days >= 1 && days <= 365;
  const setFilter = (parameter: typeof STATE_PARAM | typeof KIND_PARAM, value: string) => {
    if (
      parameter === STATE_PARAM
        ? !STATES.includes(value as LegalHoldStateFilter)
        : !FILTER_KINDS.includes(value as LegalHoldKindFilter)
    ) {
      return;
    }
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (value) next.set(parameter, value);
      else next.delete(parameter);
      next.delete(PAGE_PARAM);
      next.set(PAGE_PARAM, '1');
      next.delete(PAGE_SIZE_PARAM);
      next.set(PAGE_SIZE_PARAM, String(pager.pageSize));
      return next;
    });
  };
  const setSelected = (id: string) => {
    if (!isLegalHoldID(id)) return;
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.delete(ID_PARAM);
      next.set(ID_PARAM, id);
      return next;
    });
  };
  const closeDetail = () => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.delete(ID_PARAM);
      return next;
    });
    setReleaseDraft({ reason: '', password: '', confirmed: false });
    setConfirmation(null);
  };
  const pageData = list.data;
  const busy = session.isFetching || list.isFetching;
  const sessionError = session.error ?? (!session.data ? authorityError : null);
  const finalAuthorityError = [
    sessionError,
    authorityError,
    list.error,
    detail.error,
    create.error,
    release.error,
    elevationError,
  ].find(isFinalAuthorityLoss);
  if (finalAuthorityError) return <ErrorState error={finalAuthorityError} />;
  return (
    <div className="ops-stack">
      <Card className="ops-danger">
        <h2>{t('admin.legalHolds.title')}</h2>
        <p>{t('admin.legalHolds.description')}</p>
        <div className="ops-toolbar">
          <label>
            <span>{t('admin.legalHolds.filters.state')}</span>
            <select value={state} onChange={(event) => setFilter(STATE_PARAM, event.target.value)}>
              <option value="">{t('admin.legalHolds.filters.all')}</option>
              <option value="active">{t(STATE_LABEL_KEYS.active)}</option>
              <option value="released">{t(STATE_LABEL_KEYS.released)}</option>
              <option value="expired">{t(STATE_LABEL_KEYS.expired)}</option>
            </select>
          </label>
          <label>
            <span>{t('admin.legalHolds.filters.objectKind')}</span>
            <select value={kind} onChange={(event) => setFilter(KIND_PARAM, event.target.value)}>
              <option value="">{t('admin.legalHolds.filters.all')}</option>
              {KINDS.map((value) => (
                <option key={value} value={value}>
                  {t(KIND_LABEL_KEYS[value])}
                </option>
              ))}
            </select>
          </label>
        </div>
        {sessionError ? (
          <ErrorState error={sessionError} onRetry={() => void session.refetch()} />
        ) : !session.data || session.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : !pageData ? (
          <LoadingState />
        ) : (
          <div aria-busy={busy}>
            {busy ? <LoadingState /> : null}
            {pageData.data.length === 0 ? (
              <EmptyState
                title={t('admin.legalHolds.empty.title')}
                body={t('admin.legalHolds.empty.body')}
              />
            ) : (
              <div className="ops-table-scroll">
                <table className="ops-table ops-table--responsive">
                  <thead>
                    <tr>
                      <th>{t('admin.legalHolds.table.object')}</th>
                      <th>{t('admin.legalHolds.create.objectReference')}</th>
                      <th>{t('admin.legalHolds.table.state')}</th>
                      <th>{t('admin.legalHolds.table.window')}</th>
                      <th>{t('admin.legalHolds.table.detail')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pageData.data.map((hold) => (
                      <tr key={hold.id}>
                        <td data-label={t('admin.legalHolds.table.object')}>
                          {t(KIND_LABEL_KEYS[hold.object_kind])}
                        </td>
                        <td
                          className="ops-id ops-cell-wide"
                          data-label={t('admin.legalHolds.create.objectReference')}
                        >
                          {hold.object_ref}
                        </td>
                        <td data-label={t('admin.legalHolds.table.state')}>
                          <StatusBadge
                            active={hold.state === 'active'}
                            label={t(STATE_LABEL_KEYS[hold.state])}
                          />
                        </td>
                        <td data-label={t('admin.legalHolds.table.window')}>
                          {formatDateTime(hold.created_at)} — {formatDateTime(hold.expires_at)}
                        </td>
                        <td
                          className="ops-cell-wide"
                          data-label={t('admin.legalHolds.table.detail')}
                        >
                          <button
                            className="btn btn-secondary"
                            type="button"
                            disabled={busy}
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
            )}
            <PagePagination
              metadata={pageData.pagination}
              requestedPage={pager.page}
              busy={busy}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </div>
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
              <div className="dialog-title-row">
                <h3>{t('admin.legalHolds.detail.title')}</h3>
                <button className="btn btn-quiet" type="button" onClick={closeDetail}>
                  {t('admin.legalHolds.actions.close')}
                </button>
              </div>
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
                      release.isPending ||
                      create.isPending ||
                      elevating ||
                      busy ||
                      detail.isFetching ||
                      Boolean(list.error)
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
            release.isPending ||
            busy ||
            Boolean(list.error) ||
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
