import { rejectionFieldLabel } from './rejectionField';
import { LogReadError, logAccessLost } from '@shared/observability/logReadAccess';
import { useRetainedQueryData } from '@shared/query/useRetainedQueryData';
import { CreditNumber } from '@shared/components/CreditNumber';
import { FailureDetails, RequestDelivery } from '@shared/observability/FailureDiagnostics';
import {
  FAILURE_CATEGORIES,
  FAILURE_PHASES,
  failureCategoryKey,
  failurePhaseKey,
} from '@shared/observability/failure';
import { GatewayConnectorLabel } from '@shared/components/GatewayConnectorLabel';
import { MODEL_TYPES, MODEL_TYPE_LABEL_KEYS, type ModelType } from '@shared/modelTypes';
import { Button } from '@shared/components/ui/Button';
import { Link, useLocation } from 'react-router';
import { charityManagementPath, withLogContext } from './managementNavigation';
import { clearStationSession } from '@shared/charityManagement';
import { QueryFeedback } from '@shared/components/QueryFeedback';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { formatCredits } from '../../../user/games/common/strict';
import { Fold, Note, StatusBadge } from '@shared/components/ui';
import { RawStorageSummary } from '@shared/observability/RawStorageSummary';
import { GlobalPressure } from '@shared/observability/GlobalPressure';
import { AttemptErrors, RequestSource } from '@shared/observability/RequestDiagnostics';
import { type PageSize } from '@shared/operations/pageNumbers';
import { PagePagination } from '@shared/operations/PagePagination';
import { isCharityRoute } from '@shared/operations/requestKind';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isApiError, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useQueryClient } from '@tanstack/react-query';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import { attemptHealthSummary } from './attemptHealthText';
import { useTranslation } from 'react-i18next';
import { CallerIdentity } from './CallerIdentity';
import {
  roleLogExportPath,
  validateLogFilter,
  validLogDiscordID,
  validLogResourceID,
  logRequestID,
  type LogFiltersValue,
  type LogResultClass,
  type LogRole,
  type LogRouteKind,
  type RoleLogRow,
  type RoleLogAttempt,
} from './data';
import { InspectionWorkspace } from './InspectionWorkspace';
import { listReturnPath } from '@shared/operations/listReturn';
import { CopyValue } from '@shared/components/CopyValue';
import { LogDetailPanel } from './LogDetailPanel';
import { RequestRouteDecision } from './RequestRouteDecision';
import { LogFilters, LogSearch, type LogFilterField } from './LogFilters';
import { LogOriginIdentity } from './LogOriginIdentity';
import { LogResultBadge, logResult, logResultCopyKey } from './LogResult';
import { LogTable, type LogColumn } from './LogTable';
import {
  numberedLogKeys,
  useRoleLogDetailPage,
  useRoleLogsPage,
  type NumberedRoleLogDetail,
} from './numberedQueries';
import { rejectionDescriptionKeys } from './rejectionDescriptions';
import { TokenBuckets } from './TokenBuckets';
import { HeaderFacts } from './HeaderFacts';
import { relativeLogSeconds, useLogUrlState, type RelativeLogRange } from './useLogUrlState';
import { ATTEMPT_SEQ_PARAM, logAttemptSequence } from './managementFailures';
import {
  AttemptFailureSummary,
  AttemptHealthEffects,
  AttemptReason,
  AttemptService,
  FailureStreak,
} from './ManagementAttemptFailures';

function failedAttempt(attempt: RoleLogAttempt): boolean {
  return (
    (attempt.status_code ?? 0) >= 400 ||
    Boolean(
      attempt.error_diagnostics ||
      (attempt.result_kind === 'synthetic' && attempt.diag) ||
      (attempt.role !== 'user' &&
        (attempt.failure_disablement || attempt.health_impact?.streak_effect === 'incremented')),
    )
  );
}

const ROUTE_LABEL_KEYS: Record<LogRouteKind, string> = {
  openai_chat_completions: 'common.operations.logs.route.openaiChatCompletions',
  charity_chat_completions: 'common.operations.logs.route.charityChatCompletions',
  openai_embeddings: 'common.operations.logs.route.openaiEmbeddings',
  charity_embeddings: 'common.operations.logs.route.charityEmbeddings',
  openai_images_generations: 'common.operations.logs.route.openaiImagesGenerations',
  charity_images_generations: 'common.operations.logs.route.charityImagesGenerations',
  openai_decisions: 'common.operations.logs.route.openaiDecisions',
  charity_decisions: 'common.operations.logs.route.charityDecisions',
  openai_responses: 'common.operations.logs.route.openai_responses',
  charity_responses: 'common.operations.logs.route.charity_responses',
  openai_responses_compact: 'common.operations.logs.route.openai_responses_compact',
  charity_responses_compact: 'common.operations.logs.route.charity_responses_compact',
  openai_responses_input_tokens: 'common.operations.logs.route.openai_responses_input_tokens',
  charity_responses_input_tokens: 'common.operations.logs.route.charity_responses_input_tokens',
  openai_completions: 'common.operations.logs.route.openai_completions',
  charity_completions: 'common.operations.logs.route.charity_completions',
  openai_images_edits: 'common.operations.logs.route.openai_images_edits',
  charity_images_edits: 'common.operations.logs.route.charity_images_edits',
  openai_images_variations: 'common.operations.logs.route.openai_images_variations',
  charity_images_variations: 'common.operations.logs.route.charity_images_variations',
  openai_audio_speech: 'common.operations.logs.route.openai_audio_speech',
  charity_audio_speech: 'common.operations.logs.route.charity_audio_speech',
  openai_audio_transcriptions: 'common.operations.logs.route.openai_audio_transcriptions',
  charity_audio_transcriptions: 'common.operations.logs.route.charity_audio_transcriptions',
  openai_audio_translations: 'common.operations.logs.route.openai_audio_translations',
  charity_audio_translations: 'common.operations.logs.route.charity_audio_translations',
  openai_moderations: 'common.operations.logs.route.openai_moderations',
  charity_moderations: 'common.operations.logs.route.charity_moderations',
  openai_rerank: 'common.operations.logs.route.openai_rerank',
  charity_rerank: 'common.operations.logs.route.charity_rerank',
  openai_systemone: 'common.operations.logs.route.openai_systemone',
  charity_systemone: 'common.operations.logs.route.charity_systemone',

  model_discovery: 'common.operations.logs.route.modelDiscovery',
};

const RESULT_LABEL_KEYS: Record<LogResultClass, string> = {
  success: 'common.operations.logs.resultValue.success',
  failed: 'common.operations.logs.resultValue.failed',
  cancelled: 'common.operations.logs.resultValue.cancelled',
};

const LOG_DEFAULT_PAGE_SIZE: PageSize = 20;

function requestFrom(detail: NumberedRoleLogDetail): RoleLogRow {
  return detail.request;
}

function LogTime({ at }: { at: number }) {
  const full = useDateTimeFormatter()(at);
  return (
    <time dateTime={new Date(at * 1000).toISOString()} title={full}>
      {full}
    </time>
  );
}

function UsageMismatchBadge({ visible }: { visible: boolean }) {
  const { t } = useTranslation();

  if (!visible) return null;
  const label = t('common.operations.logs.usageTotalMismatch');
  const explanation = t('common.operations.logs.usageTotalMismatchExplanation');
  return (
    <span title={explanation} aria-label={`${label}. ${explanation}`}>
      <StatusBadge tone="warn">{label}</StatusBadge>
    </span>
  );
}

function sessionIdentity(role: LogRole, value: unknown): string | undefined {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const root = value as Record<string, unknown>;
  const nested = role === 'admin' ? root.admin : root.user;
  if (nested === null || typeof nested !== 'object' || Array.isArray(nested)) return undefined;
  const identity = nested as Record<string, unknown>;
  const valueKey = role === 'admin' ? 'username' : 'id';
  const result = identity[valueKey];
  return typeof result === 'string' && result.length > 0 ? result : undefined;
}

// Observe the authoritative session without replacing its fetch function.
function useStationScope(
  role: LogRole,
  explicitAccountID: string | undefined,
  explicitScopeReady: boolean | undefined,
) {
  const queryClient = useQueryClient();
  const sessionKey = useMemo(
    () => (role === 'admin' ? (['admin', 'session'] as const) : (['user', 'session'] as const)),
    [role],
  );
  const subscribe = useCallback(
    (notify: () => void) =>
      queryClient.getQueryCache().subscribe((event) => {
        if (
          event.query.queryKey.length === 2 &&
          event.query.queryKey[0] === sessionKey[0] &&
          event.query.queryKey[1] === 'session'
        )
          notify();
      }),
    [queryClient, sessionKey],
  );
  const snapshot = useCallback(
    () => queryClient.getQueryState(sessionKey),
    [queryClient, sessionKey],
  );
  const session = useSyncExternalStore(subscribe, snapshot, snapshot);
  const derivedAccountID = sessionIdentity(role, session?.data);
  const accountID = explicitAccountID ?? derivedAccountID ?? '';
  const scopeReady = explicitScopeReady ?? (accountID.length > 0 && !session?.error);
  return { accountID, scopeReady, sessionError: session?.error };
}

function DiagnosticValue({
  value,
  label,
  onFilter,
}: {
  value: string | null | undefined;
  label: string;
  onFilter?: (value: string) => void;
}) {
  const { t } = useTranslation();
  return value ? (
    <span className="ops-actions">
      <CopyValue value={value} label={label} />
      {onFilter && (
        <Button type="button" size="sm" onClick={() => onFilter(value)}>
          {t('common.logFilterValue')}
        </Button>
      )}
    </span>
  ) : (
    <>—</>
  );
}
function AttemptTable({
  detail,
  role,
  accountID,
  endpointKeyID,
  page,
  busy,
  onRefresh,
  onPageChange,
  onPageSizeChange,
  logReturnTo,
  focusAttemptSeq,
  locatingFocus,
  filterBy,
  canReadModels,
  readFailureMetadata,
}: {
  readFailureMetadata?: string;
  logReturnTo: string;
  detail: NumberedRoleLogDetail;
  role: LogRole;
  accountID: string;
  endpointKeyID?: string;
  page: string;
  busy: boolean;
  onRefresh: () => void;
  onPageChange: (page: string) => void;
  onPageSizeChange: (size: PageSize) => void;
  focusAttemptSeq?: string;
  locatingFocus: boolean;
  filterBy: (field: string, value: string) => void;
  canReadModels: boolean;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const location = useLocation();
  const isDiscovery = detail.request.route_kind === 'model_discovery';
  const managedCharityRequest =
    detail.request.role !== 'user' && isCharityRoute(detail.request.route_kind)
      ? detail.request
      : undefined;
  const [previousFocus, setPreviousFocus] = useState(focusAttemptSeq);
  const [open, setOpen] = useState(
    isDiscovery ||
      focusAttemptSeq !== undefined ||
      (detail.request.caller_status ?? 0) >= 400 ||
      Boolean(detail.request.caller_error_code) ||
      ('attempts' in detail && detail.attempts.data.some(failedAttempt)),
  );
  if (focusAttemptSeq !== previousFocus) {
    setPreviousFocus(focusAttemptSeq);
    if (focusAttemptSeq !== undefined) setOpen(true);
  }
  const focusTarget = useRef<HTMLElement | null>(null);
  const registerFocusTarget = useCallback((target: HTMLElement | null) => {
    focusTarget.current = target;
  }, []);
  useEffect(() => {
    if (!open || focusAttemptSeq === undefined || locatingFocus) return;
    // Let the containing detail panel finish its own initial focus and scroll.
    const frame = requestAnimationFrame(() => {
      focusTarget.current?.scrollIntoView?.({ block: 'start' });
      focusTarget.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [focusAttemptSeq, locatingFocus, open]);
  if (!('attempts' in detail)) return null;
  const attempts = detail.attempts;
  const focused = attempts.data.find((attempt) => attempt.attempt_seq === focusAttemptSeq);
  const focusMissing = focusAttemptSeq !== undefined && !locatingFocus && focused === undefined;
  const table = (
    <div className="ops-stack" aria-busy={busy}>
      {managedCharityRequest &&
        attempts.data.some(
          (a) => a.role !== 'user' && a.health_impact?.streak_effect === 'pending',
        ) && (
          <div className="ops-actions">
            <Button type="button" size="sm" disabled={busy} onClick={onRefresh}>
              {t('common.operations.logs.healthImpact.refresh')}
            </Button>
          </div>
        )}
      {focusMissing && (
        <p
          className="log-focused-attempt-heading"
          ref={registerFocusTarget}
          tabIndex={-1}
          role="status"
        >
          {t('common.operations.logs.attemptUnavailable')}
        </p>
      )}
      {locatingFocus ? (
        <p role="status">{t('common.loading')}</p>
      ) : attempts.data.length === 0 ? (
        <p>{t('common.operations.logs.noAttempts')}</p>
      ) : (
        <ol className="log-attempts">
          {attempts.data.map((attempt) => (
            <li
              className="log-attempt"
              key={attempt.attempt_seq}
              aria-current={attempt === focused || undefined}
            >
              <div className="log-attempt-heading">
                {endpointKeyID && attempt.endpoint_key_id === endpointKeyID ? (
                  <StatusBadge tone="accent">
                    {t('common.operations.logs.matchedKeyAttempt')}
                  </StatusBadge>
                ) : null}
                <h3
                  ref={attempt === focused ? registerFocusTarget : undefined}
                  tabIndex={attempt === focused ? -1 : undefined}
                >
                  {t('common.operations.logs.attemptNumber', { number: attempt.attempt_seq })}
                </h3>
                <StatusBadge tone={attempt.result_kind === 'synthetic' ? 'neutral' : 'info'}>
                  {t(
                    attempt.result_kind === 'synthetic'
                      ? isDiscovery
                        ? 'common.operations.logs.discoveryNoResponse'
                        : 'common.operations.logs.platformRecord'
                      : 'common.operations.logs.upstreamResponse',
                  )}
                </StatusBadge>
                {attempt.role !== 'user' ? (
                  <UsageMismatchBadge visible={attempt.usage_total_mismatch} />
                ) : null}
              </div>
              {attempt.role !== 'user' && attempt.failure_disablement && (
                <div className="log-attempt-disablement">
                  <Note tone="warn" title={t('common.operations.logs.failureDisablement')}>
                    <p>
                      <AttemptService observation={attempt.failure_disablement} />
                    </p>
                    <p>
                      <FailureStreak observation={attempt.failure_disablement} />
                    </p>
                  </Note>
                </div>
              )}
              {managedCharityRequest && attempt.role !== 'user' && (
                <AttemptHealthEffects attempt={attempt} />
              )}
              {attempt.result_kind === 'synthetic' ? (
                <p className="log-attempt-notice">
                  {t(
                    isDiscovery
                      ? 'common.operations.logs.discoveryNoResponseHint'
                      : 'common.operations.logs.syntheticExplanation',
                  )}
                </p>
              ) : null}
              <dl className="nb-facts log-attempt-facts">
                {attempt.role !== 'user' &&
                  attempt.error_diagnostics?.provider_reason &&
                  (!managedCharityRequest ||
                    attempt.health_impact?.backoff?.reason !==
                      attempt.error_diagnostics.provider_reason) && (
                    <div>
                      <dt>{t('common.operations.logs.failureReason')}</dt>
                      <dd>
                        <AttemptReason
                          failure={{ reason: attempt.error_diagnostics.provider_reason }}
                        />
                      </dd>
                    </div>
                  )}
                <div>
                  <dt>{t('logs.endpointBaseUrl')}</dt>
                  <dd>
                    <DiagnosticValue
                      value={attempt.endpoint_base_url}
                      label={t('logs.endpointBaseUrl')}
                      onFilter={
                        role !== 'user' ? (v) => filterBy('endpoint_base_url', v) : undefined
                      }
                    />
                  </dd>
                </div>
                {'endpoint_note' in attempt && (attempt.endpoint_note || attempt.key_note) ? (
                  <div>
                    <dt>{t('common.operations.logs.notes')}</dt>
                    <dd>
                      {attempt.endpoint_note || '—'} / {attempt.key_note || '—'}
                    </dd>
                  </div>
                ) : null}
                <div>
                  <dt>{t('common.operations.logs.endpointKeyId')}</dt>
                  <dd>
                    <DiagnosticValue
                      value={attempt.endpoint_key_id}
                      label={t('common.operations.logs.endpointKeyId')}
                      onFilter={role !== 'user' ? (v) => filterBy('endpoint_key_id', v) : undefined}
                    />
                  </dd>
                </div>
                <div>
                  <dt>{t('common.operations.logs.connectorModel')}</dt>
                  <dd>
                    <GatewayConnectorLabel value={attempt.connector_type} />
                    <br />
                    <DiagnosticValue
                      value={attempt.upstream_model_id}
                      label={t('common.operations.logs.connectorModel')}
                      onFilter={role !== 'user' ? (v) => filterBy('upstream_model', v) : undefined}
                    />
                  </dd>
                </div>
                <div>
                  <dt>
                    {t(
                      attempt.result_kind === 'synthetic' && !isDiscovery
                        ? 'common.operations.logs.recordedStatus'
                        : 'common.operations.logs.upstreamStatus',
                    )}
                  </dt>
                  <dd>
                    {isDiscovery && attempt.result_kind === 'synthetic'
                      ? t('common.operations.logs.discoveryNoResponse')
                      : (attempt.status_code ?? '—')}{' '}
                    <span className="mono">{attempt.upstream_code ?? ''}</span>
                  </dd>
                </div>
                <div>
                  <dt>{t('logs.time')}</dt>
                  <dd>
                    {formatDateTime(attempt.started_at)} ·{' '}
                    {Math.max(0, attempt.completed_at - attempt.started_at)}s
                  </dd>
                </div>
                <div>
                  <dt>{t('logs.tokens')}</dt>
                  <dd>
                    <TokenBuckets row={attempt.usage} />
                  </dd>
                </div>
              </dl>
              {attempt.diag && (
                <Fold plain title={t('logs.diag')}>
                  <p className="mono log-attempt-diagnostic">{attempt.diag}</p>
                </Fold>
              )}
              {(attempt.error_diagnostics !== undefined ||
                attempt.diag ||
                attempt.result_kind === 'synthetic' ||
                (attempt.status_code ?? 0) >= 400) && (
                <FailureDetails
                  failure={attempt.error_diagnostics}
                  showMissing={
                    attempt.role === 'user' ||
                    !managedCharityRequest ||
                    !attempt.health_impact?.backoff
                  }
                />
              )}
              {attempt.role !== 'user' && managedCharityRequest && canReadModels ? (
                managedCharityRequest.charity_model_id &&
                attempt.donation_id &&
                attempt.donation_key_id ? (
                  <div className="ops-actions">
                    <Link
                      className="nb-btn nb-btn--secondary"
                      to={withLogContext(
                        charityManagementPath(
                          attempt.role,
                          managedCharityRequest.charity_model_id,
                          {
                            donationID: attempt.donation_id,
                            keyID: attempt.donation_key_id,
                          },
                        ),
                        logReturnTo,
                      )}
                      state={{ logReturnTo, logPrivate: location.state?.logPrivate }}
                    >
                      {t('common.operations.logs.manageCharityKey')}
                    </Link>
                  </div>
                ) : (
                  <p className="nb-sub">{t('common.operations.logs.charityKeyUnavailable')}</p>
                )
              ) : null}
              {role !== 'user' ? (
                <HeaderFacts
                  role={role}
                  accountID={accountID}
                  requestID={detail.request.id}
                  attempt={Number(attempt.attempt_seq)}
                />
              ) : null}
              {role !== 'user' ? (
                <AttemptErrors
                  readMetadata={readFailureMetadata === attempt.attempt_seq}
                  role={role}
                  requestID={detail.request.id}
                  attempt={Number(attempt.attempt_seq)}
                />
              ) : null}
            </li>
          ))}
        </ol>
      )}
      {!locatingFocus && (
        <PagePagination
          metadata={detail.attempt_pagination}
          requestedPage={focusAttemptSeq === undefined ? page : undefined}
          busy={busy}
          onPageChange={onPageChange}
          onPageSizeChange={onPageSizeChange}
        />
      )}
    </div>
  );
  return (
    <Fold
      plain
      open={open}
      onOpenChange={setOpen}
      title={t('common.operations.logs.attempts')}
      summary={'attempt_count' in detail.request ? detail.request.attempt_count : undefined}
    >
      {table}
    </Fold>
  );
}

interface RoleLogPanelProps {
  role: LogRole;
  language?: string;
  canReadUsers?: boolean;
  canReadModels?: boolean;
  enabled?: boolean;
  onAuthorityLoss?: () => void;
  requestID?: string | null;
  onRequestClose?: () => void;
  accountId?: string;
  scopeReady?: boolean;
}

export function RoleLogPanel(props: RoleLogPanelProps) {
  const scope = useStationScope(props.role, props.accountId, props.scopeReady);
  const queryClient = useQueryClient();
  const previousScope = useRef({ role: props.role, accountID: scope.accountID });
  useEffect(() => {
    const previous = previousScope.current;
    previousScope.current = { role: props.role, accountID: scope.accountID };
    if (
      !previous.accountID ||
      (previous.role === props.role && previous.accountID === scope.accountID)
    )
      return;
    for (const kind of ['page', 'detail-page', 'headers']) {
      queryClient.removeQueries({
        queryKey: [...numberedLogKeys.root(previous.role), kind, previous.accountID],
      });
    }
  }, [props.role, queryClient, scope.accountID]);
  return <ScopedRoleLogPanel key={`${props.role}:${scope.accountID}`} {...props} {...scope} />;
}

function ScopedRoleLogPanel({
  canReadUsers = true,
  canReadModels = true,
  role,
  enabled = true,
  onAuthorityLoss,
  requestID,
  onRequestClose,
  accountID,
  scopeReady,
  sessionError,
}: RoleLogPanelProps & {
  accountID: string;
  scopeReady: boolean;
  sessionError: unknown;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const location = useLocation();
  const logReturnTo = location.pathname + location.search;
  const rejectionDescription = (reason: string) => {
    const counts = /^content has (\d+) characters; minimum is (\d+)$/.exec(reason);
    return counts
      ? t('logs.rejectionContentLength', { actual: counts[1], minimum: counts[2] })
      : typeof rejectionDescriptionKeys[reason] === 'string'
        ? t(rejectionDescriptionKeys[reason])
        : reason;
  };
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const station = role === 'admin' ? 'admin' : role === 'steward' ? 'steward' : 'user';
  // Preserve pagination storage while time inputs use the steward context.
  const pageStation = role === 'admin' ? 'admin' : 'user';
  const keyParams = searchParams.getAll('endpoint_key_id');
  const invalidKeyFilter =
    role !== 'user' &&
    keyParams.length > 0 &&
    (keyParams.length !== 1 || !validLogResourceID(keyParams[0]));
  const discordParams = searchParams.getAll('discord_id');
  const invalidDiscordFilter =
    role !== 'user' &&
    discordParams.length > 0 &&
    (discordParams.length !== 1 || !validLogDiscordID(discordParams[0]));
  const invalidFilter = invalidKeyFilter || invalidDiscordFilter;
  const textParams = useMemo(
    () =>
      role === 'user'
        ? (['outcome', 'operation', 'model', 'error_code', 'status', 'phase'] as const)
        : ([
            'operation',
            'user_id',
            'discord_id',
            'endpoint_key_id',
            'endpoint_base_url',
            'upstream_model',
            'charity_model',
            'error_code',
            'status',
            'phase',
            'usage_total_mismatch',
            'outcome',
            'error_category',
            'error_phase',
          ] as const),
    [role],
  );
  const { state: urlState, patch: patchUrlState } = useLogUrlState(
    textParams,
    LOG_DEFAULT_PAGE_SIZE,
    role + ':' + accountID,
  );
  const [windowEnd, setWindowEnd] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (!urlState.range) return;
    const updateWindow = () => setWindowEnd(Math.floor(Date.now() / 1000));
    updateWindow();
    window.addEventListener('focus', updateWindow);
    return () => window.removeEventListener('focus', updateWindow);
  }, [urlState.range]);
  const filter = useMemo(() => {
    const next = validateLogFilter(role, urlState.filters);
    const from = urlState.range
      ? windowEnd - relativeLogSeconds[urlState.range]
      : urlState.fromUnix;
    const to = urlState.range ? windowEnd : urlState.toUnix;
    return {
      ...next,
      ...(from === undefined ? {} : { from }),
      ...(to === undefined ? {} : { to }),
    } satisfies LogFiltersValue;
  }, [role, urlState.filters, urlState.fromUnix, urlState.toUnix, urlState.range, windowEnd]);
  const filterKey = useMemo(() => JSON.stringify(filter), [filter]);
  const pager = useUrlPagePager({
    station: pageStation,
    listType: 'logs',
    scopeKey: accountID,
    scopeReady,
    resetKey: `${role}:${filterKey}`,
  });
  const requestIDValues = searchParams.getAll('request_id');
  const urlRequestID = logRequestID(requestIDValues.length === 1 ? requestIDValues[0] : null);
  const [selectedID, setSelectedID] = useState<string | null>(requestID ?? urlRequestID);
  const previousURLID = useRef(urlRequestID);
  const previousPropID = useRef(requestID);
  useEffect(() => {
    if (urlRequestID !== previousURLID.current) {
      previousURLID.current = urlRequestID;
      setSelectedID(urlRequestID);
    }
  }, [urlRequestID]);
  useEffect(() => {
    if (requestID === undefined || requestID === previousPropID.current) return;
    previousPropID.current = requestID;
    setSelectedID(requestID);
  }, [requestID]);
  const attemptPager = useUrlPagePager({
    station: pageStation,
    listType: 'log-attempts',
    // Opening a request clears its nested URL window below. Deep links and
    // browser history already carry that request's window; closing the panel
    // must not enqueue a new page reset after those parameters were removed.
    scopeKey: accountID,
    scopeReady,
    pageParam: 'attempt_page',
    pageSizeParam: 'attempt_page_size',
    clearOnWindowChange: ATTEMPT_SEQ_PARAM,
  });
  const attemptSeqValues = searchParams.getAll(ATTEMPT_SEQ_PARAM);
  const focusAttemptSeq =
    role !== 'user' &&
    selectedID !== null &&
    selectedID === urlRequestID &&
    attemptSeqValues.length === 1
      ? logAttemptSequence(attemptSeqValues[0])
      : undefined;
  const [revoked, setRevoked] = useState(false);
  const [revokedError, setRevokedError] = useState<unknown>(null);
  const authorityClosedRef = useRef(false);
  const observerEnabled = enabled && scopeReady && !revoked;
  const listEnabled = observerEnabled && !invalidFilter;
  const logs = useRoleLogsPage(role, accountID, pager.page, pager.pageSize, filter, listEnabled);
  const detail = useRoleLogDetailPage(
    role,
    accountID,
    selectedID,
    attemptPager.page,
    attemptPager.pageSize,
    observerEnabled,
    focusAttemptSeq,
  );
  const authorityError =
    revokedError ??
    (logAccessLost(sessionError)
      ? sessionError
      : logAccessLost(logs.error)
        ? logs.error
        : logAccessLost(detail.error)
          ? detail.error
          : null);
  const retainedLogData = useRetainedQueryData(
    { data: logs.data, error: authorityError ?? logs.error },
    [role, accountID],
    listEnabled,
  );
  const authorityClosed = revoked || authorityError !== null;

  useEffect(() => {
    if (!authorityError || authorityClosedRef.current) return;
    authorityClosedRef.current = true;
    setRevoked(true);
    setRevokedError(authorityError);
    setSelectedID(null);
    patchUrlState(
      { filters: {}, fromUnix: undefined, toUnix: undefined },
      { clearDetail: true, replace: true },
    );
    for (const kind of ['page', 'detail-page', 'headers']) {
      queryClient.removeQueries({ queryKey: [...numberedLogKeys.root(role), kind, accountID] });
    }
    if (isUnauthorized(authorityError))
      clearStationSession(queryClient, role === 'admin' ? 'admin' : 'steward');
    onAuthorityLoss?.();
  }, [accountID, authorityError, onAuthorityLoss, patchUrlState, queryClient, role]);

  const fields = useMemo<readonly LogFilterField[]>(() => {
    const values: LogFilterField[] = [
      {
        name: 'outcome',
        label: t('logs.outcomeFilter'),
        ariaLabel: t('logs.outcomeFilter'),
        options: [
          { value: '', label: t('common.all') },
          { value: 'failed', label: t('logs.finalFailure') },
          { value: 'success', label: t('logs.finalSuccess') },
          ...(role === 'user'
            ? []
            : [
                { value: 'before_dispatch', label: t('logs.beforeDispatch') },
                { value: 'actual_backoff', label: t('logs.actualBackoff') },
              ]),
        ],
      },
      {
        name: 'operation',
        label: t('logs.operation'),
        ariaLabel: t('logs.operation'),
        options: [
          { value: '', label: t('common.all') },
          ...MODEL_TYPES.map((value) => ({ value, label: t(MODEL_TYPE_LABEL_KEYS[value]) })),
          { value: 'model_discovery', label: t('common.operations.logs.route.modelDiscovery') },
        ],
      },
      {
        name: 'phase',
        label: t('logs.phase'),
        ariaLabel: t('logs.phase'),
        options: [
          { value: '', label: t('logs.phaseAll') },
          { value: 'pre_handler', label: t('logs.phasePreHandler') },
          { value: 'handler', label: t('logs.phaseHandler') },
        ],
      },
    ];
    if (role === 'user')
      values.push({
        name: 'model',
        label: t('common.model'),
        ariaLabel: t('common.model'),
        placeholder: t('common.operations.logs.presentation.modelPlaceholder'),
        maxLength: 133,
      });
    if (role !== 'user')
      values.push({
        name: 'user_id',
        label: t('common.userId'),
        ariaLabel: t('common.userId'),
        placeholder: t('common.operations.logs.presentation.userPlaceholder'),
        inputMode: 'numeric',
      });
    if (role !== 'user') {
      values.push(
        {
          name: 'error_category',
          label: t('common.failure.categoryLabel'),
          ariaLabel: t('common.failure.categoryLabel'),
          options: [
            { value: '', label: t('common.failure.allCategories') },
            ...FAILURE_CATEGORIES.map((value) => ({ value, label: t(failureCategoryKey(value)) })),
          ],
        },
        {
          name: 'error_phase',
          label: t('common.failure.phaseLabel'),
          ariaLabel: t('common.failure.phaseLabel'),
          options: [
            { value: '', label: t('common.failure.allPhases') },
            ...FAILURE_PHASES.map((value) => ({ value, label: t(failurePhaseKey(value)) })),
          ],
        },
      );
      values.push({
        name: 'discord_id',
        label: t('logs.callerDiscordId'),
        ariaLabel: t('logs.callerDiscordId'),
        placeholder: t('common.operations.logs.discordFilterHint'),
        inputMode: 'numeric',
      });
      values.push({
        name: 'usage_total_mismatch',
        label: t('common.operations.logs.usageTotalMismatchFilter'),
        ariaLabel: t('common.operations.logs.usageTotalMismatchFilter'),
        options: [
          { value: '', label: t('common.operations.logs.usageTotalMismatchAll') },
          { value: 'true', label: t('common.operations.logs.usageTotalMismatchOnly') },
        ],
      });
      values.push({
        name: 'endpoint_key_id',
        label: t('common.operations.logs.endpointKeyId'),
        ariaLabel: t('common.operations.logs.endpointKeyId'),
        inputMode: 'numeric',
      });
      values.push({
        name: 'endpoint_base_url',
        label: t('logs.endpointBaseUrl'),
        ariaLabel: t('logs.endpointBaseUrl'),
        maxLength: 512,
      });
      values.push({
        name: 'upstream_model',
        label: t('logs.upstreamModel'),
        ariaLabel: t('logs.upstreamModel'),
        placeholder: t('common.operations.logs.presentation.upstreamPlaceholder'),
        maxLength: 512,
      });
      values.push({
        name: 'charity_model',
        label: t('common.operations.logs.charityModelFilter'),
        ariaLabel: t('common.operations.logs.charityModelFilter'),
        placeholder: t('common.operations.logs.charityModelFilterHint'),
        maxLength: 512,
      });
    }
    values.push({
      name: 'error_code',
      label: t('logs.errorCode'),
      ariaLabel: t('logs.errorCode'),
      maxLength: 96,
    });
    values.push({
      name: 'status',
      label: t('logs.httpStatus'),
      ariaLabel: t('logs.httpStatus'),
      inputMode: 'numeric',
      placeholder: t('logs.statusExample'),
    });
    return values;
  }, [role, t]);
  const routeLabel = (route: LogRouteKind) => t(ROUTE_LABEL_KEYS[route]);
  const resultLabel = (row: RoleLogRow) =>
    logResult(row).key === 'discoveryAccepted'
      ? t('common.operations.logs.discoveryAccepted')
      : row.caller_result_class
        ? t(RESULT_LABEL_KEYS[row.caller_result_class])
        : t('common.operations.logs.resultValue.pending');

  const alertOrigin =
    role === 'admin' && searchParams.has('return_to')
      ? listReturnPath({ returnTo: searchParams.get('return_to') }, '/alerts')
      : undefined;
  const [lookupReset, setLookupReset] = useState(0);
  const [jump, setJump] = useState<{ request: string; seq: string }>();
  const clearDetailURL = () => {
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (!alertOrigin) next.delete('return_to');
        next.delete('request_id');
        next.delete('attempt_page');
        next.delete('attempt_page_size');
        next.delete(ATTEMPT_SEQ_PARAM);
        const actualPage = logs.data?.pagination.page;
        if (actualPage !== undefined) next.set('page', actualPage);
        next.set('page_size', String(pager.pageSize));
        return next;
      },
      { replace: true },
    );
  };
  const closeDetail = () => {
    setJump(undefined);
    setSelectedID(null);
    onRequestClose?.();
    clearDetailURL();
  };
  const openDetail = (id: string) => {
    setJump(undefined);
    setSelectedID(id);
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (!alertOrigin) next.delete('return_to');
      next.set('request_id', id);
      const actualPage = logs.data?.pagination.page;
      if (actualPage !== undefined) next.set('page', actualPage);
      next.set('page_size', String(pager.pageSize));
      next.delete('attempt_page');
      next.delete('attempt_page_size');
      next.delete(ATTEMPT_SEQ_PARAM);
      return next;
    });
  };
  const applyFilters = (
    next: {
      filters: Record<string, string>;
      fromUnix?: number;
      toUnix?: number;
      range?: RelativeLogRange;
    },
    keepFilters?: readonly string[],
  ) => {
    setSelectedID(null);
    patchUrlState({ ...next, page: 1 }, { clearDetail: true, keepFilters });
  };

  const filterBy = (field: string, value: string) => {
    if (!textParams.some((name) => name === field)) return;
    applyFilters({ ...urlState, filters: { ...urlState.filters, [field]: value } });
  };
  const routeSummary = (route: LogRouteKind) =>
    route === 'model_discovery'
      ? t('common.operations.logs.presentation.models')
      : route.endsWith('chat_completions')
        ? t('common.operations.logs.presentation.chat')
        : route.endsWith('embeddings')
          ? t('common.operations.logs.presentation.embeddings')
          : route.endsWith('images_generations')
            ? t('common.operations.logs.presentation.images')
            : t(MODEL_TYPE_LABEL_KEYS[route.replace(/^(openai|charity)_/, '') as ModelType]);
  const modelCell = (row: RoleLogRow) => {
    const model = 'model' in row ? row.model : row.charity_model;
    const kind =
      row.route_kind === 'model_discovery'
        ? routeSummary(row.route_kind)
        : t(
            row.route_kind.startsWith('charity_')
              ? 'common.operations.logs.presentation.charity'
              : 'common.operations.logs.presentation.personal',
          ) +
          ' · ' +
          routeSummary(row.route_kind);
    const charity = row.route_kind.startsWith('charity_');
    const mode =
      charity && (row.requested_streaming == null ? t('logs.modeUnknown') : requestMode(row));
    return (
      <span className="log-model">
        {model && (
          <span className="mono" title={model}>
            {model}
          </span>
        )}
        <span className={model ? 'nb-sub' : undefined}>
          {kind}
          {mode && (
            <>
              {' '}
              · <span>{mode}</span>
            </>
          )}
        </span>
      </span>
    );
  };
  const usageCell = (row: RoleLogRow) => {
    if (row.usage.usage_unknown) return '—';
    const input = (
      BigInt(row.usage.uncached_input_tokens) +
      BigInt(row.usage.cache_read_input_tokens) +
      BigInt(row.usage.cache_write_input_tokens)
    ).toString();
    return (
      <span className="log-usage">
        {t('common.operations.logs.presentation.usage', { input, output: row.usage.output_tokens })}
        {row.usage.cache_read_input_tokens !== '0' || row.usage.cache_write_input_tokens !== '0' ? (
          <span className="nb-sub">
            {[
              row.usage.cache_read_input_tokens !== '0'
                ? t('common.operations.logs.presentation.cacheRead', {
                    tokens: row.usage.cache_read_input_tokens,
                  })
                : '',
              row.usage.cache_write_input_tokens !== '0'
                ? t('common.operations.logs.presentation.cacheWrite', {
                    tokens: row.usage.cache_write_input_tokens,
                  })
                : '',
            ]
              .filter(Boolean)
              .join(' · ')}
          </span>
        ) : null}
      </span>
    );
  };
  const requestMode = (row: RoleLogRow) =>
    t(
      row.requested_streaming == null
        ? 'common.operations.logs.facts.unknown'
        : row.requested_streaming
          ? 'common.operations.logs.facts.streaming'
          : 'common.operations.logs.facts.nonStreaming',
    );
  const actionLabels = {
    short_content_deduction: t('common.operations.logs.facts.short_content_deduction'),
    short_content_ban: t('common.operations.logs.facts.short_content_ban'),
    short_content_suspension: t('common.operations.logs.facts.short_content_suspension'),
    rpm_ban: t('common.operations.logs.facts.rpm_ban'),
    client_rule_ban: t('common.operations.logs.facts.client_rule_ban'),
    context_penalty: t('common.operations.logs.facts.context_penalty'),
  };
  const actionText = (row: RoleLogRow) =>
    row.automatic_actions == null
      ? t('common.operations.logs.facts.unknown')
      : row.automatic_actions.length === 0
        ? t('common.operations.logs.facts.noActions')
        : row.automatic_actions.map((action) => actionLabels[action]).join(' · ');
  const columns: LogColumn<RoleLogRow>[] = [
    {
      key: 'time',
      header: t('logs.time'),
      cell: 'meta',
      align: 'nowrap',
      render: (row) => <LogTime at={row.started_at} />,
    },
    ...(role !== 'user'
      ? [
          {
            key: 'user',
            header: t('common.history.originalIdentity'),
            cell: 'title' as const,
            render: (row: RoleLogRow) =>
              row.role !== 'user' ? (
                <LogOriginIdentity
                  value={row}
                  role={row.role}
                  canOpen={canReadUsers}
                  compact
                  nickname={
                    row.origin_discord_id &&
                    row.origin_discord_id === row.caller_identity?.discord_id
                      ? row.caller_identity.discord_nickname
                      : null
                  }
                />
              ) : (
                '—'
              ),
          },
        ]
      : []),
    {
      key: 'model',
      header: t('common.model'),
      cell: role === 'user' ? 'title' : 'meta',
      render: modelCell,
    },
    {
      key: 'result',
      header: t('common.operations.logs.result'),
      cell: 'status',
      render: (row) => (
        <>
          <LogResultBadge row={row} />
          {row.route_kind.startsWith('charity_') && row.automatic_actions?.length !== 0 && (
            <span className="nb-sub">
              {row.automatic_actions == null ? t('logs.actionsUnknown') : actionText(row)}
            </span>
          )}
        </>
      ),
    },
    {
      key: 'elapsed',
      header: t('common.logDuration'),
      cell: 'meta',
      render: (row) => (
        <>
          {row.completed_at === null
            ? '—'
            : t('common.operations.logs.presentation.seconds', {
                seconds: row.completed_at - row.started_at,
              })}
          {'attempt_count' in row && (
            <span className="nb-sub">
              {t('common.operations.logs.attempts')} · {row.attempt_count}
            </span>
          )}
        </>
      ),
    },
    { key: 'usage', header: t('logs.tokens'), cell: 'meta', align: 'num', render: usageCell },
    {
      key: 'charge',
      header: t('common.operations.logs.charge'),
      cell: 'meta',
      align: 'num',
      render: (row) => (
        <span className="nb-amount">
          <CreditNumber value={formatCredits(row.usage.charge)} />
        </span>
      ),
    },
  ];

  const modelManagement = (row: RoleLogRow) =>
    canReadModels && row.role !== 'user' && row.route_kind.startsWith('charity_') ? (
      row.charity_model_id ? (
        <Link
          className="nb-btn nb-btn--secondary"
          to={withLogContext(charityManagementPath(row.role, row.charity_model_id), logReturnTo)}
          state={{ logReturnTo, logPrivate: location.state?.logPrivate }}
        >
          {t('common.operations.logs.manageCharityModel')}
        </Link>
      ) : (
        <span className="nb-sub">{t('common.operations.logs.charityModelUnavailable')}</span>
      )
    ) : null;

  const resultHint = (row: RoleLogRow, hasFailureSummary: boolean) => {
    const result = logResult(row);
    if (!result.hint) return null;
    if (['keyHint', 'rateHint', 'retryHint'].includes(result.hint)) {
      if (row.role === 'user' && row.route_kind.startsWith('charity_'))
        return t('common.operations.logs.presentation.charityUpstreamHint');
      if (hasFailureSummary) return null;
    }
    const hintKeys = {
      creditsHint: 'common.operations.logs.presentation.creditsHint',
      keyHint: 'common.operations.logs.presentation.keyHint',
      rateHint: 'common.operations.logs.presentation.rateHint',
      retryHint: 'common.operations.logs.presentation.retryHint',
      discoveryAcceptedHint: 'common.operations.logs.discoveryAcceptedHint',
    } as const;
    return t(hintKeys[result.hint]);
  };

  let detailBody: ReactNode = null;
  if (selectedID && detail.isPending) detailBody = t('common.loading');
  else if (selectedID && detail.error)
    detailBody =
      isApiError(detail.error) && detail.error.code === 'not_found' ? (
        <p role="status">
          {t(
            role === 'user'
              ? 'common.operations.logs.requestUnavailable'
              : focusAttemptSeq !== undefined
                ? 'common.operations.logs.attemptUnavailable'
                : 'logs.lookupMissing',
          )}
        </p>
      ) : (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      );

  const detailData = detail.error ? undefined : detail.data;
  const detailRequest = detailData ? requestFrom(detailData) : null;
  const failureGroups =
    role !== 'user' && detailData && 'failure_summary' in detailData
      ? detailData.failure_summary
      : undefined;
  const attemptSummaries =
    detailData && 'attempts' in detailData
      ? detailData.attempts.data.map((attempt) => {
          const status =
            attempt.status_code == null
              ? undefined
              : attempt.result_kind === 'response'
                ? 'HTTP ' + attempt.status_code
                : t('common.operations.logs.recordedStatus') + ' ' + attempt.status_code;
          return [
            t('common.operations.logs.attemptNumber', { number: attempt.attempt_seq }),
            attempt.result_kind === 'synthetic'
              ? t('common.operations.logs.platformRecord')
              : undefined,
            status,
            attempt.upstream_model_id,
            attempt.role !== 'user' && detailRequest && isCharityRoute(detailRequest.route_kind)
              ? attemptHealthSummary(attempt, t, formatDateTime)
              : undefined,
          ]
            .filter(Boolean)
            .join(' · ');
        })
      : [];
  const detailFields =
    detailData && detailRequest
      ? [
          {
            label: t('common.operations.logs.request'),
            value: <CopyValue value={detailRequest.id} label={t('logs.lookupLabel')} />,
          },
          ...(role !== 'user' &&
          (detailRequest.role === 'admin' || detailRequest.role === 'steward')
            ? [
                {
                  label: t('logs.charityModel'),
                  value: detailRequest.charity_model ?? '—',
                },
                {
                  label: t('common.history.originalIdentity'),
                  value: (
                    <LogOriginIdentity
                      canOpen={canReadUsers}
                      value={detailRequest}
                      role={detailRequest.role}
                    />
                  ),
                },
                {
                  label: t('logs.caller'),
                  value: <CallerIdentity identity={detailRequest.caller_identity} />,
                },
              ]
            : []),
          { label: t('logs.time'), value: formatDateTime(detailRequest.started_at) },
          {
            label: t('common.model'),
            value:
              'model' in detailRequest ? detailRequest.model : (detailRequest.charity_model ?? '—'),
          },
          {
            label: t('common.operations.logs.presentation.duration'),
            value:
              detailRequest.completed_at === null
                ? '—'
                : t('common.operations.logs.presentation.seconds', {
                    seconds: detailRequest.completed_at - detailRequest.started_at,
                  }),
          },
          { label: t('logs.routeKind'), value: routeLabel(detailRequest.route_kind) },
          ...(detailRequest.route_kind.startsWith('charity_')
            ? [
                {
                  label: t('common.operations.logs.facts.mode'),
                  value: requestMode(detailRequest),
                },
                {
                  label: t('common.operations.logs.facts.actions'),
                  value: actionText(detailRequest),
                },
              ]
            : []),
          {
            label: t('logs.phase'),
            value: t(
              detailRequest.phase === 'pre_handler' ? 'logs.phasePreHandler' : 'logs.phaseHandler',
            ),
          },
          ...(detailRequest.phase === 'pre_handler'
            ? [
                {
                  label: t('logs.rejectionStage'),
                  value:
                    detailRequest.rejection_stage === null
                      ? '—'
                      : t(
                          {
                            authorization: 'logs.rejectionStages.authorization',
                            flow: 'logs.rejectionStages.flow',
                            preflight: 'logs.rejectionStages.preflight',
                          }[detailRequest.rejection_stage],
                        ),
                },
                { label: t('logs.rejectionReason'), value: detailRequest.rejection_reason },
              ]
            : []),
          ...(detailRequest.phase === 'pre_handler'
            ? [
                {
                  label: t('logs.requestRoute'),
                  value: `${detailRequest.request_method} ${detailRequest.request_path}`,
                },
              ]
            : []),
          {
            label: t('common.operations.logs.callerResult'),
            technical: true,
            value: `${resultLabel(detailRequest)} / ${detailRequest.caller_status ?? '—'}`,
          },
          {
            label: t('common.operations.logs.callerError'),
            technical: true,
            value: (
              <DiagnosticValue
                value={detailRequest.caller_error_code}
                label={t('logs.errorCode')}
                onFilter={(v) => filterBy('error_code', v)}
              />
            ),
          },
          {
            label: t('logs.tokens'),
            value: (
              <div className="ops-stack">
                <TokenBuckets row={detailRequest.usage} />
                {detailRequest.role !== 'user' ? (
                  <UsageMismatchBadge visible={detailRequest.usage_total_mismatch} />
                ) : null}
              </div>
            ),
          },
          ...(role !== 'user'
            ? [
                {
                  label: t('common.audit.source'),
                  wide: true,
                  value: (
                    <RequestSource
                      key={`${accountID}:${detailRequest.id}`}
                      role={role}
                      requestID={detailRequest.id}
                    />
                  ),
                },
              ]
            : []),
          {
            label: t('common.operations.logs.charge'),
            value: (
              <span className="nb-amount">
                <CreditNumber value={formatCredits(detailRequest.usage.charge)} />
              </span>
            ),
          },
          ...('kind' in detailData && detailData.kind === 'charity'
            ? [
                {
                  label: t('common.operations.logs.result'),
                  value: t(RESULT_LABEL_KEYS[detailData.caller_safe_result.class]),
                },
              ]
            : []),
        ]
      : detailBody
        ? [{ label: t('logs.details'), value: detailBody }]
        : [];

  const title =
    role === 'admin'
      ? t('admin.logs.logsTitle')
      : role === 'user'
        ? t('user.logs.title')
        : t('common.operations.logs.stewardTitle');
  const empty =
    role === 'admin'
      ? t('admin.logs.noLogs')
      : role === 'user'
        ? t('user.logs.empty')
        : t('common.operations.logs.stewardEmpty');
  const emptyBody =
    role === 'admin'
      ? t('admin.logs.noLogsBody')
      : role === 'user'
        ? t('user.logs.emptyBody')
        : t('common.operations.logs.stewardEmptyBody');

  if (authorityClosed) {
    return (
      <Card>
        <ErrorState error={authorityError ?? revokedError} />
      </Card>
    );
  }

  if (!enabled || !scopeReady || !accountID)
    return (
      <Card>
        <LoadingState />
      </Card>
    );

  const pageData = invalidFilter ? undefined : retainedLogData;
  const listBusy = logs.isFetching;
  const selectedIndex = pageData?.data.findIndex((row) => row.id === selectedID) ?? -1;
  const exportMenu = !invalidFilter ? (
    <details className="nb-more log-export">
      <summary className="nb-btn nb-btn--secondary nb-btn--sm">
        {t('common.operations.logs.presentation.export')} ▾
      </summary>
      <div className="nb-more__menu">
        {(['csv', 'json'] as const).map((format) => (
          <a key={format} href={roleLogExportPath(role, filter, format)} download>
            {t(
              format === 'csv'
                ? 'common.operations.logs.presentation.exportCsv'
                : 'common.operations.logs.presentation.exportJson',
            )}
          </a>
        ))}
      </div>
    </details>
  ) : null;
  return (
    <Card className={`ops-stack log-panel log-panel--${role}`}>
      {role === 'steward' ? (
        <div className="card-title-row">
          <h2>{title}</h2>
          {exportMenu}
        </div>
      ) : null}
      {role !== 'user' ? <GlobalPressure role={role} accountID={accountID} /> : null}
      {alertOrigin && !selectedID && <Link to={alertOrigin}>{t('logs.returnToAlert')}</Link>}
      <InspectionWorkspace
        selected={selectedID}
        title={t('logs.drawerTitle') + (selectedID ? ' ' + selectedID : '')}
        closeLabel={t('common.close')}
        onClose={closeDetail}
        detail={
          <LogReadError value={setRevokedError}>
            {pageData && selectedIndex >= 0 && (
              <div className="ops-actions">
                <Button
                  type="button"
                  disabled={selectedIndex === 0}
                  onClick={() => openDetail(pageData.data[selectedIndex - 1].id)}
                >
                  {t('common.logAdjacentPrevious')}
                </Button>
                <Button
                  type="button"
                  disabled={selectedIndex + 1 >= pageData.data.length}
                  onClick={() => openDetail(pageData.data[selectedIndex + 1].id)}
                >
                  {t('common.logAdjacentNext')}
                </Button>
              </div>
            )}
            {alertOrigin && (
              <Link className="nb-btn nb-btn--secondary" to={alertOrigin}>
                {t('logs.returnToAlert')}
              </Link>
            )}
            <LogDetailPanel
              key={selectedID}
              fields={detailFields}
              headers={
                role !== 'user' && detailRequest ? (
                  <HeaderFacts role={role} accountID={accountID} requestID={detailRequest.id} />
                ) : null
              }
              diagnostics={
                detailRequest
                  ? {
                      label: t('common.operations.logs.request'),
                      text: [
                        detailRequest.id,
                        formatDateTime(detailRequest.started_at),
                        resultLabel(detailRequest),
                        detailRequest.request_path,
                        detailRequest.caller_error_code,
                        detailRequest.caller_status,
                        detailRequest.rejection_detail?.field,
                        detailRequest.rejection_detail &&
                          rejectionDescription(detailRequest.rejection_detail.reason),
                        ...attemptSummaries,
                      ]
                        .filter((value) => value != null)
                        .join('\n'),
                    }
                  : undefined
              }
              result={
                detailRequest ? (
                  <>
                    <Note
                      tone={logResult(detailRequest).tone}
                      title={t(
                        logResultCopyKey(
                          role,
                          logResult(detailRequest).key,
                          detailRequest.rejection_reason ?? detailRequest.caller_error_code,
                        ),
                        {
                          status: detailRequest.caller_status ?? '—',
                        },
                      )}
                    >
                      {detailRequest.phase === 'pre_handler' && (
                        <p>
                          {t('logs.stoppedBeforeDispatch', { status: detailRequest.caller_status })}
                        </p>
                      )}
                      {(detailRequest.phase === 'pre_handler' ||
                        detailRequest.rejection_detail) && (
                        <div className="log-outcome__reason">
                          <strong>{t('logs.rejectionDetail')}</strong>
                          <p>
                            {detailRequest.rejection_detail
                              ? `${rejectionFieldLabel(detailRequest.rejection_detail.field, t)}: ${rejectionDescription(detailRequest.rejection_detail.reason)}`
                              : t('logs.rejectionDetailMissing')}
                          </p>
                        </div>
                      )}
                      {detailRequest.rejection_detail?.suggestion ? (
                        <div className="log-outcome__reason">
                          <strong>{t('logs.rejectionSuggestion')}</strong>
                          <p>{rejectionDescription(detailRequest.rejection_detail.suggestion)}</p>
                        </div>
                      ) : (
                        resultHint(detailRequest, Boolean(failureGroups?.length))
                      )}
                    </Note>
                    {Boolean(failureGroups?.length) && (
                      <AttemptFailureSummary groups={failureGroups} />
                    )}
                    {detailData && 'route_decision' in detailData && detailData.route_decision && (
                      <RequestRouteDecision decision={detailData.route_decision} />
                    )}
                    {detailData && 'attempts' in detailData && (
                      <>
                        {attemptSummaries.length > 0 && (
                          <ol className="log-attempt-summary" aria-label={t('logs.attemptSummary')}>
                            {attemptSummaries.map((line, index) => (
                              <li key={index}>
                                {failedAttempt(detailData.attempts.data[index]) ? (
                                  <button
                                    type="button"
                                    className="nb-btn nb-btn--ghost"
                                    onClick={() =>
                                      setJump({
                                        request: detailRequest.id,
                                        seq: detailData.attempts.data[index].attempt_seq,
                                      })
                                    }
                                  >
                                    {line}
                                  </button>
                                ) : (
                                  line
                                )}
                              </li>
                            ))}
                          </ol>
                        )}
                        <AttemptTable
                          key={detailRequest.id}
                          detail={detailData}
                          filterBy={filterBy}
                          logReturnTo={logReturnTo}
                          role={role}
                          accountID={accountID}
                          canReadModels={canReadModels}
                          endpointKeyID={role === 'user' ? undefined : filter.endpoint_key_id}
                          page={attemptPager.page}
                          busy={detail.isFetching}
                          onRefresh={() => void detail.refetch()}
                          readFailureMetadata={
                            jump?.request === detailRequest.id ? jump.seq : undefined
                          }
                          focusAttemptSeq={
                            jump?.request === detailRequest.id ? jump.seq : focusAttemptSeq
                          }
                          locatingFocus={focusAttemptSeq !== undefined && detail.isPlaceholderData}
                          onPageChange={attemptPager.setPage}
                          onPageSizeChange={attemptPager.setPageSize}
                        />
                      </>
                    )}
                    {(detailRequest.phase !== 'pre_handler' ||
                      (detailRequest.role !== 'user' && detailRequest.charity_model_id)) &&
                      modelManagement(detailRequest)}
                    <Fold
                      plain
                      title={t('common.failure.deliveryTitle')}
                      defaultOpen={detailData?.delivery?.failed === true}
                      key={detailRequest.id}
                    >
                      <RequestDelivery
                        delivery={detailData?.delivery}
                        result={resultLabel(detailRequest)}
                      />
                    </Fold>
                  </>
                ) : null
              }
            />
          </LogReadError>
        }
      >
        <LogSearch onOpenRequest={openDetail} resetKey={lookupReset}>
          <LogFilters
            station={station}
            fields={fields}
            state={urlState}
            linkErrors={
              invalidFilter
                ? {
                    ...(invalidDiscordFilter
                      ? { discord_id: t('common.operations.logs.invalidDiscordFilter') }
                      : {}),
                    ...(invalidKeyFilter
                      ? { endpoint_key_id: t('common.operations.logs.invalidKeyFilter') }
                      : {}),
                  }
                : undefined
            }
            onResetLookup={() => setLookupReset((value) => value + 1)}
            onApply={applyFilters}
            toolbarActions={
              <>
                <Button
                  type="button"
                  disabled={listBusy || invalidFilter}
                  onClick={() => {
                    const now = Math.floor(Date.now() / 1000);
                    if (urlState.range && now !== windowEnd) setWindowEnd(now);
                    else void logs.refetch();
                  }}
                >
                  {t('common.refresh')}
                </Button>
                {role !== 'steward' ? exportMenu : null}
              </>
            }
          />
        </LogSearch>
        {role !== 'user' && filter.endpoint_key_id ? (
          <p className="inline-notice" role="status">
            {t('common.operations.logs.keyFilterNotice', { id: filter.endpoint_key_id })}
          </p>
        ) : null}
        {!invalidFilter ? (
          <QueryFeedback
            query={{
              ...logs,
              data: pageData,
              retained: logs.data === undefined && pageData !== undefined,
            }}
          />
        ) : null}
        {invalidFilter ? null : logs.error && !pageData ? (
          <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
        ) : pageData ? (
          <div className="ops-stack" aria-busy={listBusy}>
            {logs.error ? (
              <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
            ) : null}
            {pageData.data.length === 0 ? (
              <EmptyState title={empty} body={emptyBody} />
            ) : (
              <LogTable
                caption={title}
                columns={columns}
                rows={pageData.data}
                rowKey={(row) => row.id}
                actions={(row) => (
                  <div className="ops-actions">
                    <Button
                      type="button"
                      data-inspect-id={row.id}
                      data-inspect-trigger=""
                      aria-current={selectedID === row.id || undefined}
                      onClick={() => openDetail(row.id)}
                    >
                      {t('logs.details')}
                    </Button>
                    {row.role !== 'user' && row.charity_model_id ? modelManagement(row) : null}
                  </div>
                )}
              />
            )}
            <PagePagination
              metadata={pageData.pagination}
              requestedPage={logs.data === undefined ? undefined : pager.page}
              busy={listBusy}
              onPageChange={(page) => {
                if (logs.error && page === pager.page) void logs.refetch();
                else pager.setPage(page);
              }}
              onPageSizeChange={(size) => {
                if (logs.error && size === pager.pageSize) void logs.refetch();
                else pager.setPageSize(size);
              }}
            />
          </div>
        ) : logs.isPending ? (
          <LoadingState />
        ) : (
          <LoadingState />
        )}
        {role !== 'user' ? <RawStorageSummary key={`${role}:${accountID}`} role={role} /> : null}
      </InspectionWorkspace>
    </Card>
  );
}
