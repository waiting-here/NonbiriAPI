import { useTranslation } from 'react-i18next';
import { Fold, Note } from '@shared/components/ui';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import {
  failureCategoryKey,
  failurePhaseKey,
  type DeliveryDiagnostics,
  type FailureDiagnostics,
} from './failure';
import './observability.css';

const REASON_CODES = new Set([
  'dns_timeout',
  'dns_not_found',
  'dns_failed',
  'tls_unknown_authority',
  'tls_hostname_mismatch',
  'tls_invalid_certificate',
  'tls_record_invalid',
  'canceled',
  'deadline_exceeded',
  'network_timeout',
  'compression_header_invalid',
  'compression_checksum_invalid',
  'unexpected_eof',
  'connection_reset',
  'connection_refused',
  'connection_closed',
  'broken_pipe',
  'connect_failed',
  'read_failed',
  'write_failed',
  'invalid_json',
  'json_type_mismatch',
  'invalid_response',
  'client_write_failed',
  'response_limit',
  'upstream_error',
]);
const SUGGESTION_CATEGORIES = new Set([
  'network',
  'policy',
  'tls',
  'timeout',
  'upstream',
  'protocol',
  'encoding',
  'resource_limit',
  'canceled',
  'delivery',
  'platform',
]);

export function FailureDetails({ failure }: { failure?: FailureDiagnostics | null }) {
  const { t } = useTranslation();
  const formatTime = useDateTimeFormatter();
  const missing = t('common.failure.notRecorded');
  const flag = (value?: boolean) =>
    value === undefined ? missing : t(value ? 'common.yes' : 'common.no');
  const count = (value?: number) => (value === undefined ? missing : formatCount(value).display);
  if (!failure)
    return (
      <p className="nb-small nb-muted">
        {t(failure === null ? 'common.failure.unavailable' : 'common.failure.missing')}
      </p>
    );
  const facts = [
    ['common.failure.headers', flag(failure.headers_received)],
    ['common.failure.eof', flag(failure.body_eof)],
    ['common.failure.usage', flag(failure.usage_present)],
    ['common.failure.receivedBytes', count(failure.received_bytes)],
    ['common.failure.receivedEvents', count(failure.received_events)],
    ['common.failure.forwardedBytes', count(failure.forwarded_bytes)],
    ['common.failure.budget', count(failure.budget_ms)],
    ['common.failure.retryAfter', count(failure.retry_after_seconds)],
    ['common.failure.limitBytes', count(failure.limit_bytes)],
    ['common.failure.mediaType', failure.content_type ?? missing],
    ['common.failure.encoding', failure.content_encoding ?? missing],
    ['common.failure.limitKind', failure.limit_kind ?? missing],
  ];
  const times = [
    ['common.failure.startedAt', failure.started_at_ms],
    ['common.failure.firstBodyAt', failure.first_body_at_ms],
    ['common.failure.failedAt', failure.failed_at_ms],
    ['common.failure.completedAt', failure.completed_at_ms],
  ] as const;
  return (
    <section
      className="request-diagnostics failure-diagnostics"
      aria-label={t('common.failure.title')}
    >
      <Note
        tone="warn"
        title={t(
          failure.code && REASON_CODES.has(failure.code)
            ? `common.failure.reason.${failure.code}`
            : failureCategoryKey(failure.category),
        )}
      >
        <p>{t('common.failure.atPhase', { phase: t(failurePhaseKey(failure.phase)) })}</p>
        <p>
          {t(
            `common.failure.suggestion.${SUGGESTION_CATEGORIES.has(failure.category ?? '') ? failure.category : 'unknown'}`,
          )}
        </p>
      </Note>
      {failure.response_started === true && <p>{t('common.failure.afterStarted')}</p>}
      <dl>
        <div>
          <dt>{t('common.failure.upstreamStatus')}</dt>
          <dd>{failure.upstream_status ?? t('common.failure.noUpstreamStatus')}</dd>
        </div>
        <div>
          <dt>{t('common.failure.protocol')}</dt>
          <dd>
            {failure.protocol_complete === undefined
              ? missing
              : t(
                  failure.protocol_complete
                    ? 'common.failure.complete'
                    : 'common.failure.incomplete',
                )}
          </dd>
        </div>
      </dl>
      <Fold plain title={t('common.failure.technical')}>
        <p className="nb-small nb-muted">{t('common.failure.observationLimit')}</p>
        {failure.incomplete && <p role="status">{t('common.failure.partial')}</p>}
        <dl>
          <div>
            <dt>{t('common.failure.categoryLabel')}</dt>
            <dd>
              {t(failureCategoryKey(failure.category))}{' '}
              <span className="mono">{failure.category ?? ''}</span>
            </dd>
          </div>
          <div>
            <dt>{t('common.failure.phaseLabel')}</dt>
            <dd className="mono">{failure.phase ?? missing}</dd>
          </div>
          <div>
            <dt>{t('common.failure.code')}</dt>
            <dd className="mono">{failure.code ?? missing}</dd>
          </div>
          <div>
            <dt>{t('common.failure.safeMessage')}</dt>
            <dd>{failure.message ?? missing}</dd>
          </div>
          {facts.map(([key, value]) => (
            <div key={key}>
              <dt>{t(key)}</dt>
              <dd
                className={
                  [
                    'common.failure.receivedBytes',
                    'common.failure.receivedEvents',
                    'common.failure.forwardedBytes',
                    'common.failure.budget',
                    'common.failure.retryAfter',
                    'common.failure.limitBytes',
                  ].includes(key)
                    ? 'nb-amount'
                    : undefined
                }
              >
                {value}
              </dd>
            </div>
          ))}
          {times.map(([key, value]) => {
            const date = value === undefined ? undefined : new Date(value);
            const iso = date && !Number.isNaN(date.getTime()) ? date.toISOString() : undefined;
            return (
              <div key={key}>
                <dt>{t(key)}</dt>
                <dd>
                  {iso ? (
                    <time dateTime={iso} title={iso}>
                      {formatTime(iso)} <span className="mono">({iso})</span>
                    </time>
                  ) : (
                    missing
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
        {(
          [
            ['causes', 'common.failure.causes'],
            ['secondary', 'common.failure.secondary'],
          ] as const
        ).map(([key, label]) => (
          <div key={key}>
            <h4>{t(label)}</h4>
            {failure[key].length ? (
              <ol>
                {failure[key].map((cause, index) => (
                  <li key={index}>
                    <span className="mono">
                      {[cause.type, cause.code].filter(Boolean).join(' · ')}
                    </span>
                    {cause.message && <p>{cause.message}</p>}
                  </li>
                ))}
              </ol>
            ) : (
              <p>{missing}</p>
            )}
          </div>
        ))}
        <h4>{t('common.failure.correlation')}</h4>
        {failure.correlation.length ? (
          <dl>
            {failure.correlation.map((id, index) => (
              <div key={index}>
                <dt>
                  <span className="mono">
                    {id.source} · {id.name}
                  </span>
                </dt>
                <dd className="mono">{id.value}</dd>
              </div>
            ))}
          </dl>
        ) : (
          <p>{missing}</p>
        )}
        <h4>{t('common.failure.timings')}</h4>
        {failure.timings.length ? (
          <ol>
            {failure.timings.map((timing, index) => (
              <li key={index}>
                {t(failurePhaseKey(timing.phase))} <span className="mono">({timing.phase})</span> ·{' '}
                <span className="nb-amount">{count(timing.elapsed_ms)} ms</span>
              </li>
            ))}
          </ol>
        ) : (
          <p>{missing}</p>
        )}
      </Fold>
    </section>
  );
}

export function RequestDelivery({
  delivery,
  result,
}: {
  delivery?: DeliveryDiagnostics | null;
  result: string;
}) {
  const { t } = useTranslation();
  return (
    <section
      className="request-diagnostics failure-diagnostics"
      aria-label={t('common.failure.deliveryTitle')}
    >
      <dl>
        <div>
          <dt>{t('common.failure.finalResult')}</dt>
          <dd>{result}</dd>
        </div>
        <div>
          <dt>{t('common.failure.actualHttp')}</dt>
          <dd>{delivery?.http_status ?? t('common.failure.notRecorded')}</dd>
        </div>
        {delivery && (
          <div>
            <dt>{t('common.failure.deliveryBytes')}</dt>
            <dd>
              {delivery.bytes === undefined ? (
                t('common.failure.notRecorded')
              ) : (
                <span className="nb-amount">{formatCount(delivery.bytes).display} B</span>
              )}
            </dd>
          </div>
        )}
      </dl>
      <p className="nb-small nb-muted">{t('common.failure.deliveryLimit')}</p>
      {!delivery ? (
        <p>
          {t(delivery === null ? 'common.failure.unavailable' : 'common.failure.deliveryMissing')}
        </p>
      ) : delivery.incomplete ? (
        <p>{t('common.failure.partial')}</p>
      ) : null}
      {delivery?.failed === true && <FailureDetails failure={delivery.failure} />}
    </section>
  );
}
