import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import { clearStationSession, type CharityManagementFrame } from '@shared/charityManagement';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  getRecurringLimits,
  putRecurringLimits,
  recurringLimitsDraftKey,
  recurringLimitsKeys,
  type RecurringLimitAlignment,
  type RecurringLimitInterval,
  type RecurringLimitMetric,
  type RecurringLimitMode,
  type RecurringLimitRole,
  type RecurringLimitRuleInput,
  type RecurringLimitRuleView,
  type RecurringLimitsResponse,
  type RecurringLimitsWritePayload,
  type RecurringLimitsWriteReceipt,
} from '@shared/operations/recurringLimits';
import { browserTimeZone, fetchTimeZones, type TimeStation } from '@shared/time';
import { formatDateTime } from '@shared/utils/datetime';
import { ErrorState, LoadingState } from './States';
import { copyForRecurringLimits, type RecurringLimitsCopy } from './recurringLimitsCopy';
import {
  recurringLimitDraftFromView,
  recurringLimitRuleStructureChanged,
  type RecurringLimitDraft,
} from './recurringLimitsState';
import './recurringLimits.css';

const MAX_RULES = 16;
const U128_MAX = (1n << 128n) - 1n;
const CREDIT_AMOUNT = /^(0|[1-9][0-9]*)(?:\.([0-9]{0,2}[1-9]))?$/;
const DECIMAL = /^(0|[1-9][0-9]*)$/;
const TIME_ZONE = /^[A-Za-z0-9_+/-]{1,64}$/;

type DraftRule = RecurringLimitDraft;

interface RuleComparison {
  authority: RecurringLimitsResponse;
  attempted: RecurringLimitsWritePayload;
}

interface RecurringLimitsProps {
  role: RecurringLimitRole;
  donationId: string;
  keyId: string;
  accountId: string;
  readOnly?: boolean;
  onSaved?: (receipt: RecurringLimitsWriteReceipt) => void;
  onCapabilityLoss?: () => void;
}

interface RuleCardProps {
  copy: RecurringLimitsCopy;
  draft: DraftRule;
  index: number;
  total: number;
  view?: RecurringLimitRuleView;
  editable: boolean;
  disabled: boolean;
  expanded: boolean;
  zones: readonly string[];
  serverNow: number;
  locale: 'en' | 'zh';
  onToggle: () => void;
  onChange: (update: Partial<RecurringLimitRuleInput>) => void;
  onDelete: () => void;
  onMove: (direction: -1 | 1) => void;
}

function draftFromResponse(response: RecurringLimitsResponse): DraftRule[] {
  return response.rules.map((rule) => recurringLimitDraftFromView(rule));
}

function payloadRuleFromDraft(rule: DraftRule): RecurringLimitRuleInput {
  return {
    id: rule.id,
    mode: rule.mode,
    interval: rule.interval,
    alignment: rule.alignment,
    time_zone: rule.time_zone,
    week_starts_on: rule.week_starts_on,
    metric: rule.metric,
    limit: rule.limit,
  };
}

function payloadRulesFromDraft(rules: readonly DraftRule[]): RecurringLimitRuleInput[] {
  return rules.map(payloadRuleFromDraft);
}

function sameRule(a: RecurringLimitRuleInput, b: RecurringLimitRuleInput): boolean {
  return !recurringLimitRuleStructureChanged(a, b) && a.id === b.id && a.limit === b.limit;
}

function sameRuleSet(
  baseline: readonly RecurringLimitRuleInput[],
  draft: readonly RecurringLimitRuleInput[],
): boolean {
  return (
    baseline.length === draft.length &&
    baseline.every((rule, index) => {
      const next = draft[index];
      return next !== undefined && sameRule(rule, next);
    })
  );
}

function canonicalDecimal(value: string): boolean {
  if (!DECIMAL.test(value)) return false;
  try {
    return BigInt(value) <= U128_MAX;
  } catch {
    return false;
  }
}

function canonicalCredits(value: string): boolean {
  const match = CREDIT_AMOUNT.exec(value);
  if (!match) return false;
  try {
    return BigInt(match[1]) * 1_000n + BigInt((match[2] ?? '').padEnd(3, '0') || '0') <= U128_MAX;
  } catch {
    return false;
  }
}

function validTimeZone(value: string, zones: readonly string[]): boolean {
  return (
    TIME_ZONE.test(value) &&
    value !== 'Local' &&
    !value.startsWith('/') &&
    !value.includes('..') &&
    zones.includes(value)
  );
}

function validCombination(rule: RecurringLimitRuleInput): boolean {
  if (rule.mode === 'sliding') return rule.alignment === null && rule.week_starts_on === null;
  if (rule.alignment === null || (rule.interval === '5h' && rule.alignment === 'calendar'))
    return false;
  if (rule.alignment === 'calendar' && rule.interval === 'week') {
    return rule.week_starts_on !== null && rule.week_starts_on >= 1 && rule.week_starts_on <= 7;
  }
  return rule.week_starts_on === null;
}

function draftValidationError(
  rules: readonly DraftRule[],
  zones: readonly string[] | undefined,
  copy: RecurringLimitsCopy,
): string | undefined {
  if (rules.length > MAX_RULES) return copy.maxRules;
  for (const rule of rules) {
    if (rule.limit === '') return copy.missingLimit;
    if (rule.metric === 'credits' ? !canonicalCredits(rule.limit) : !canonicalDecimal(rule.limit)) {
      return copy.invalidLimit;
    }
    if (zones && !validTimeZone(rule.time_zone, zones)) return copy.invalidTimeZone;
    if (!validCombination(rule)) return copy.invalidCombination;
  }
  return undefined;
}

function newDraftRule(id: string, zone: string): DraftRule {
  return {
    draftId: id,
    id: null,
    mode: 'reset',
    interval: '5h',
    alignment: 'first_success',
    time_zone: zone,
    week_starts_on: null,
    metric: 'calls',
    limit: '',
  };
}

function displayInteger(value: string | null | undefined): string {
  if (value === null || value === undefined) return '—';
  try {
    const parsed = BigInt(value);
    const negative = parsed < 0n;
    const digits = (negative ? -parsed : parsed).toString();
    let grouped = '';
    for (let end = digits.length; end > 0; end -= 3) {
      const start = Math.max(0, end - 3);
      grouped = `${digits.slice(start, end)}${grouped ? `,${grouped}` : ''}`;
    }
    return `${negative ? '-' : ''}${grouped}`;
  } catch {
    return '—';
  }
}

function displayMetric(value: string | null | undefined, metric: RecurringLimitMetric): string {
  if (value === null || value === undefined) return '—';
  if (metric !== 'credits') return displayInteger(value);
  const split = value.split('.');
  return `${displayInteger(split[0])}${split[1] ? `.${split[1]}` : ''}`;
}

function metricWithUnit(
  value: string | null | undefined,
  metric: RecurringLimitMetric,
  copy: RecurringLimitsCopy,
): string {
  const rendered = displayMetric(value, metric);
  if (rendered === '—') return rendered;
  return copy.formatMetricValue(rendered, metric);
}

function progressPercent(view: RecurringLimitRuleView | undefined): number {
  if (!view) return 0;
  try {
    const limit =
      view.metric === 'credits'
        ? BigInt(view.limit.split('.')[0] ?? '0') * 1_000n +
          BigInt((view.limit.split('.')[1] ?? '').padEnd(3, '0') || '0')
        : BigInt(view.limit);
    const used =
      view.metric === 'credits'
        ? BigInt(view.used.split('.')[0] ?? '0') * 1_000n +
          BigInt((view.used.split('.')[1] ?? '').padEnd(3, '0') || '0')
        : BigInt(view.used);
    const reserved =
      view.metric === 'credits'
        ? BigInt(view.reserved.split('.')[0] ?? '0') * 1_000n +
          BigInt((view.reserved.split('.')[1] ?? '').padEnd(3, '0') || '0')
        : BigInt(view.reserved);
    if (limit <= 0n) return used + reserved > 0n ? 100 : 0;
    const hundredths = Number(((used + reserved) * 10_000n) / limit) / 100;
    return Math.min(100, Math.max(0, Number.isFinite(hundredths) ? hundredths : 0));
  } catch {
    return 0;
  }
}

function offsetAt(epoch: number, zone: string): string | null {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      timeZoneName: 'shortOffset',
    }).formatToParts(new Date(epoch * 1000));
    const name = parts.find((part) => part.type === 'timeZoneName')?.value;
    if (name) return name.replace(/^GMT/, 'UTC');
  } catch {
    // The selected zone is validated against the server registry before save.
  }
  return null;
}

function localInZone(epoch: number, zone: string, locale: 'en' | 'zh'): string {
  try {
    return new Intl.DateTimeFormat(locale === 'zh' ? 'zh-CN' : 'en-US', {
      dateStyle: 'medium',
      timeStyle: 'medium',
      timeZone: zone,
    }).format(new Date(epoch * 1000));
  } catch {
    return `${epoch}s`;
  }
}

function zoneHint(
  zone: string,
  serverNow: number,
  locale: 'en' | 'zh',
  copy: RecurringLimitsCopy,
): string {
  return copy.zoneExample(
    zone,
    offsetAt(serverNow, zone) ?? copy.offsetUnavailable,
    localInZone(serverNow, zone, locale),
  );
}

function timeValue(
  epoch: number | null,
  zone: string,
  locale: 'en' | 'zh',
  copy: RecurringLimitsCopy,
  kind: 'period' | 'transition',
): string {
  if (epoch === null) return copy.noPeriod;
  const business = `${localInZone(epoch, zone, locale)} (${zone} ${offsetAt(epoch, zone) ?? copy.offsetUnavailable})`;
  const browser = `${copy.browserTime}: ${formatDateTime(epoch)}`;
  return kind === 'period'
    ? copy.periodValue(business, browser)
    : copy.transitionValue(business, browser);
}

function stateClass(state: string | undefined): string {
  if (state === 'limited') return 'recurring-limits__state--limited';
  if (state === 'waiting_first_success') return 'recurring-limits__state--waiting';
  return 'recurring-limits__state--available';
}

function RuleUsage({
  copy,
  view,
  draft,
  locale,
}: {
  copy: RecurringLimitsCopy;
  view?: RecurringLimitRuleView;
  draft: DraftRule;
  locale: 'en' | 'zh';
}) {
  if (!view) {
    return <p className="recurring-limits__new-note">{copy.preserveUsage}</p>;
  }
  const period =
    view.period_start !== null && view.period_end !== null
      ? `${timeValue(view.period_start, view.time_zone, locale, copy, 'period')} — ${timeValue(view.period_end, view.time_zone, locale, copy, 'period')}`
      : copy.noPeriod;
  const next =
    view.next_transition_at === null
      ? draft.mode === 'sliding'
        ? copy.slidingRecovery
        : copy.noPeriod
      : timeValue(view.next_transition_at, view.time_zone, locale, copy, 'transition');
  return (
    <div className="recurring-limits__usage" aria-label={copy.usage}>
      <dl className="recurring-limits__metrics">
        <div>
          <dt>{copy.used}</dt>
          <dd title={view.used}>{metricWithUnit(view.used, view.metric, copy)}</dd>
        </div>
        <div>
          <dt>{copy.reserved}</dt>
          <dd title={view.reserved}>{metricWithUnit(view.reserved, view.metric, copy)}</dd>
        </div>
        <div>
          <dt>{copy.remaining}</dt>
          <dd title={view.remaining}>{metricWithUnit(view.remaining, view.metric, copy)}</dd>
        </div>
        <div>
          <dt>{copy.limit}</dt>
          <dd title={view.limit}>{metricWithUnit(view.limit, view.metric, copy)}</dd>
        </div>
      </dl>
      <div
        className="recurring-limits__progress"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={progressPercent(view)}
        aria-valuetext={`${copy.used}: ${metricWithUnit(view.used, view.metric, copy)}; ${copy.reserved}: ${metricWithUnit(view.reserved, view.metric, copy)}; ${copy.remaining}: ${metricWithUnit(view.remaining, view.metric, copy)}`}
      >
        <span style={{ width: `${progressPercent(view)}%` }} />
      </div>
      {view.state === 'waiting_first_success' ? (
        <p className="recurring-limits__state-help">{copy.waitingFirstSuccess}</p>
      ) : null}
      {view.mode === 'reset' && view.period_start !== null && view.period_end !== null ? (
        <>
          <p className="recurring-limits__time">
            <strong>{copy.period}:</strong> {period}
          </p>
          <p className="recurring-limits__time">
            {view.alignment === 'calendar' ? copy.naturalPeriod : copy.firstSuccessPeriod}
          </p>
        </>
      ) : null}
      <p className="recurring-limits__time">
        <strong>{copy.nextTransition}:</strong> {next}
      </p>
    </div>
  );
}

function ruleSummary(
  copy: RecurringLimitsCopy,
  ruleLabel: string,
  draft: DraftRule,
  view: RecurringLimitRuleView | undefined,
): string {
  const usageMetric = view?.metric ?? draft.metric;
  const startsAt =
    draft.mode === 'reset'
      ? copy.alignmentValue[draft.alignment ?? 'first_success']
      : copy.slidingStart;
  const weekStart =
    draft.mode === 'reset' && draft.alignment === 'calendar' && draft.interval === 'week'
      ? `${copy.weekStartsOn}: ${copy.weekValue(draft.week_starts_on ?? 1)}`
      : null;
  return [
    ruleLabel,
    `${copy.metric}: ${copy.metricValue[draft.metric]}`,
    `${copy.mode}: ${copy.modeValue[draft.mode]}`,
    `${copy.interval}: ${copy.intervalValue[draft.interval]}`,
    `${copy.alignment}: ${startsAt}`,
    weekStart,
    `${copy.timeZone}: ${draft.time_zone || '—'}`,
    `${copy.limit}: ${metricWithUnit(draft.limit || null, draft.metric, copy)}`,
    `${copy.used}: ${metricWithUnit(view?.used, usageMetric, copy)}`,
    `${copy.reserved}: ${metricWithUnit(view?.reserved, usageMetric, copy)}`,
    `${copy.remaining}: ${metricWithUnit(view?.remaining, usageMetric, copy)}`,
  ]
    .filter((part): part is string => part !== null)
    .join(' · ');
}

function RuleCard({
  copy,
  draft,
  index,
  total,
  view,
  editable,
  disabled,
  expanded,
  zones,
  serverNow,
  locale,
  onToggle,
  onChange,
  onDelete,
  onMove,
}: RuleCardProps) {
  const zoneListId = useId();
  const zoneInputId = `${zoneListId}-input`;
  const ruleLabel = copy.rule(index + 1);
  const summaryState = view?.state;
  const summary = ruleSummary(copy, ruleLabel, draft, view);
  const alignmentOptions: RecurringLimitAlignment[] =
    draft.interval === '5h' ? ['first_success'] : ['first_success', 'calendar'];
  const intervalOptions: RecurringLimitInterval[] = ['5h', 'day', 'week', 'month'];
  const metricOptions: RecurringLimitMetric[] = ['calls', 'tokens', 'credits'];
  const modeOptions: RecurringLimitMode[] = ['reset', 'sliding'];
  const firstSuccess = draft.alignment === 'first_success';
  const calendarWeek =
    draft.mode === 'reset' && draft.alignment === 'calendar' && draft.interval === 'week';
  return (
    <section className="recurring-limits__rule" data-rule-id={draft.id ?? draft.draftId}>
      <div className="recurring-limits__rule-summary">
        <button
          type="button"
          className="recurring-limits__toggle"
          aria-expanded={expanded}
          aria-label={`${expanded ? copy.collapse : copy.expand}: ${summary}`}
          onClick={onToggle}
        >
          <span>{expanded ? '▾' : '▸'}</span>
          <span className="recurring-limits__summary-text">{summary}</span>
          {summaryState ? (
            <span className={`recurring-limits__state ${stateClass(summaryState)}`}>
              {copy.state[summaryState]}
            </span>
          ) : (
            <span className="recurring-limits__state">
              {draft.id === null ? copy.unsavedRule : copy.ruleId(draft.id)}
            </span>
          )}
        </button>
        {editable ? (
          <div className="recurring-limits__rule-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={disabled || index === 0}
              onClick={() => onMove(-1)}
            >
              {copy.moveUp}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={disabled || index === total - 1}
              onClick={() => onMove(1)}
            >
              {copy.moveDown}
            </button>
            <button type="button" className="btn btn-danger" disabled={disabled} onClick={onDelete}>
              {copy.remove}
            </button>
          </div>
        ) : null}
      </div>
      {expanded ? (
        <div className="recurring-limits__rule-body">
          <RuleUsage copy={copy} view={view} draft={draft} locale={locale} />
          {editable ? (
            <div className="recurring-limits__fields">
              <label>
                <span>{copy.metric}</span>
                <select
                  value={draft.metric}
                  disabled={disabled}
                  onChange={(event) =>
                    onChange({ metric: event.target.value as RecurringLimitMetric })
                  }
                >
                  {metricOptions.map((value) => (
                    <option key={value} value={value}>
                      {copy.metricValue[value]}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>{copy.mode}</span>
                <select
                  value={draft.mode}
                  disabled={disabled}
                  onChange={(event) => {
                    const mode = event.target.value as RecurringLimitMode;
                    onChange(
                      mode === 'sliding'
                        ? { mode, alignment: null, week_starts_on: null }
                        : {
                            mode,
                            alignment: 'first_success',
                            week_starts_on: null,
                          },
                    );
                  }}
                >
                  {modeOptions.map((value) => (
                    <option key={value} value={value}>
                      {copy.modeValue[value]}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>{copy.interval}</span>
                <select
                  value={draft.interval}
                  disabled={disabled}
                  onChange={(event) => {
                    const interval = event.target.value as RecurringLimitInterval;
                    const alignment =
                      draft.mode === 'sliding'
                        ? null
                        : interval === '5h'
                          ? 'first_success'
                          : (draft.alignment ?? 'first_success');
                    onChange({
                      interval,
                      alignment,
                      week_starts_on:
                        alignment === 'calendar' && interval === 'week'
                          ? (draft.week_starts_on ?? 1)
                          : null,
                    });
                  }}
                >
                  {intervalOptions.map((value) => (
                    <option key={value} value={value}>
                      {copy.intervalValue[value]}
                    </option>
                  ))}
                </select>
              </label>
              {draft.mode === 'reset' ? (
                <label>
                  <span>{copy.alignment}</span>
                  <select
                    value={firstSuccess ? 'first_success' : 'calendar'}
                    disabled={disabled}
                    onChange={(event) => {
                      const alignment = event.target.value as RecurringLimitAlignment;
                      onChange({
                        alignment,
                        week_starts_on:
                          alignment === 'calendar' && draft.interval === 'week' ? 1 : null,
                      });
                    }}
                  >
                    {alignmentOptions.map((value) => (
                      <option key={value} value={value}>
                        {copy.alignmentValue[value]}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {calendarWeek ? (
                <label>
                  <span>{copy.weekStartsOn}</span>
                  <select
                    value={String(draft.week_starts_on ?? 1)}
                    disabled={disabled}
                    onChange={(event) => onChange({ week_starts_on: Number(event.target.value) })}
                  >
                    {[1, 2, 3, 4, 5, 6, 7].map((value) => (
                      <option key={value} value={value}>
                        {copy.weekValue(value)}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              <div className="recurring-limits__zone-field">
                <label htmlFor={zoneInputId}>{copy.timeZone}</label>
                <input
                  id={zoneInputId}
                  aria-describedby={`${zoneInputId}-hint`}
                  value={draft.time_zone}
                  list={zoneListId}
                  maxLength={64}
                  autoComplete="off"
                  placeholder={copy.selectZone}
                  disabled={disabled}
                  onChange={(event) => onChange({ time_zone: event.target.value })}
                />
                <datalist id={zoneListId}>
                  {zones.map((zone) => (
                    <option key={zone} value={zone} />
                  ))}
                </datalist>
                <small id={`${zoneInputId}-hint`}>
                  {zoneHint(draft.time_zone, serverNow, locale, copy)}
                </small>
              </div>
              <label>
                <span>{copy.limit}</span>
                <input
                  type="text"
                  inputMode={draft.metric === 'credits' ? 'decimal' : 'numeric'}
                  value={draft.limit}
                  maxLength={128}
                  required
                  placeholder={copy.limitPlaceholder}
                  disabled={disabled}
                  onChange={(event) => onChange({ limit: event.target.value })}
                />
              </label>
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function structuralChangeMessages(
  baseline: readonly RecurringLimitRuleInput[],
  draft: readonly RecurringLimitRuleInput[],
  copy: RecurringLimitsCopy,
): string[] {
  const messages: string[] = [];
  const baselineByID = new Map(
    baseline.filter((rule) => rule.id !== null).map((rule) => [rule.id as string, rule]),
  );
  const draftIDs = new Set(
    draft.filter((rule) => rule.id !== null).map((rule) => rule.id as string),
  );
  draft.forEach((rule, index) => {
    if (rule.id === null) messages.push(copy.structuralAdded(index + 1));
    else if (
      baselineByID.has(rule.id) &&
      recurringLimitRuleStructureChanged(baselineByID.get(rule.id)!, rule)
    )
      messages.push(copy.structuralReset(index + 1));
  });
  baseline.forEach((rule) => {
    if (rule.id !== null && !draftIDs.has(rule.id)) messages.push(copy.structuralDeleted);
  });
  const baselineIDs = baseline.map((rule) => rule.id).filter((id): id is string => id !== null);
  const draftExistingIDs = draft.map((rule) => rule.id).filter((id): id is string => id !== null);
  if (
    baselineIDs.length === draftExistingIDs.length &&
    baselineIDs.some((id, index) => draftExistingIDs[index] !== id)
  )
    messages.push(copy.structuralOrder);
  if (
    baseline.some((rule) => {
      const next = draft.find((candidate) => candidate.id !== null && candidate.id === rule.id);
      return next !== undefined && rule.limit !== next.limit;
    })
  )
    messages.push(copy.structuralLimit);
  return [...new Set(messages)];
}

function comparisonLines(
  rules: readonly RecurringLimitRuleView[] | readonly RecurringLimitRuleInput[],
  copy: RecurringLimitsCopy,
): string[] {
  if (rules.length === 0) return [copy.noRules];
  return rules.map((rule, index) => {
    const id = rule.id === null ? copy.unsavedRule : copy.ruleId(rule.id);
    const startsAt =
      rule.alignment === null ? copy.slidingStart : copy.alignmentValue[rule.alignment];
    return [
      `${copy.rule(index + 1)} · ${id}`,
      `${copy.metric}: ${copy.metricValue[rule.metric]}`,
      `${copy.mode}: ${copy.modeValue[rule.mode]}`,
      `${copy.interval}: ${copy.intervalValue[rule.interval]}`,
      `${copy.alignment}: ${startsAt}`,
      `${copy.timeZone}: ${rule.time_zone}`,
      `${copy.weekStartsOn}: ${copy.weekStartValue(rule.week_starts_on)}`,
      `${copy.limit}: ${metricWithUnit(rule.limit, rule.metric, copy)}`,
    ].join(' · ');
  });
}

function ConflictPanel({
  copy,
  comparison,
  attempted,
  onUseCurrent,
  onKeepDraft,
}: {
  copy: RecurringLimitsCopy;
  comparison: RuleComparison;
  attempted: readonly RecurringLimitRuleInput[];
  onUseCurrent: () => void;
  onKeepDraft: () => void;
}) {
  return (
    <aside className="recurring-limits__conflict" role="alert">
      <h4>{copy.conflictTitle}</h4>
      <p>{copy.conflictBody}</p>
      <div className="recurring-limits__comparison">
        <div>
          <strong>{copy.conflictAuthority}</strong>
          <ul>
            {comparisonLines(comparison.authority.rules, copy).map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
        <div>
          <strong>{copy.conflictDraft}</strong>
          <ul>
            {comparisonLines(attempted, copy).map((line, index) => (
              <li key={`${line}-${index}`}>{line}</li>
            ))}
          </ul>
        </div>
      </div>
      <div className="recurring-limits__conflict-actions">
        <button type="button" className="btn btn-secondary" onClick={onUseCurrent}>
          {copy.useCurrent}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onKeepDraft}>
          {copy.keepDraft}
        </button>
      </div>
    </aside>
  );
}

function ownerStation(role: RecurringLimitRole): TimeStation {
  return role === 'admin' ? 'admin' : 'user';
}

function authorityFrame(role: RecurringLimitRole): CharityManagementFrame {
  return role === 'admin' ? 'admin' : 'steward';
}

function readOrWriteError(error: unknown): boolean {
  return isUnauthorized(error) || isForbidden(error);
}

function scopeChanged(
  previous: { role: RecurringLimitRole; accountId: string; donationId: string; keyId: string },
  current: { role: RecurringLimitRole; accountId: string; donationId: string; keyId: string },
): boolean {
  return (
    previous.role !== current.role ||
    previous.accountId !== current.accountId ||
    previous.donationId !== current.donationId ||
    previous.keyId !== current.keyId
  );
}

export function RecurringLimits({
  role,
  donationId,
  keyId,
  accountId,
  readOnly = false,
  onSaved,
  onCapabilityLoss,
}: RecurringLimitsProps) {
  const { i18n } = useTranslation();
  const copy = copyForRecurringLimits(i18n.language);
  const locale: 'en' | 'zh' = i18n.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
  const editable = !readOnly && (role === 'admin' || role === 'steward');
  const client = useQueryClient();
  const scope = recurringLimitsDraftKey(role, accountId, donationId, keyId);
  const [renderedScope, setRenderedScope] = useState(scope);
  const identity = useMemo(
    () => ({ role, accountId, donationId, keyId }),
    [accountId, donationId, keyId, role],
  );
  const scopeRef = useRef(identity);
  const loadedDataRef = useRef<RecurringLimitsResponse | null>(null);
  const [draftRules, setDraftRules] = useState<DraftRule[] | null>(null);
  const [baseline, setBaseline] = useState<RecurringLimitsResponse | null>(null);
  const [expectedRevision, setExpectedRevision] = useState<string | null>(null);
  const [comparison, setComparison] = useState<RuleComparison | null>(null);
  const [attemptedPayload, setAttemptedPayload] = useState<RecurringLimitsWritePayload | null>(
    null,
  );
  const [attemptScope, setAttemptScope] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [unknownScope, setUnknownScope] = useState<string | null>(null);
  const [comparisonUnavailable, setComparisonUnavailable] = useState(false);
  const [authoritySyncPending, setAuthoritySyncPending] = useState(false);
  const [authoritySyncUnavailable, setAuthoritySyncUnavailable] = useState(false);
  const [saveFeedback, setSaveFeedback] = useState(false);
  const [savedScope, setSavedScope] = useState<string | null>(null);
  const [capabilityRevoked, setCapabilityRevoked] = useState(false);
  const draftID = useRef(0);
  const operationRef = useRef<{
    scope: string;
    payload: RecurringLimitsWritePayload;
  } | null>(null);
  const notifiedReceiptRef = useRef<RecurringLimitsWriteReceipt | null>(null);
  const read = useQuery({
    queryKey: recurringLimitsKeys.detail(role, accountId, donationId, keyId),
    queryFn: ({ signal }) => getRecurringLimits(role, donationId, keyId, signal),
    enabled:
      !capabilityRevoked &&
      role !== undefined &&
      accountId !== '' &&
      donationId !== '' &&
      keyId !== '',
    retry: false,
    staleTime: 5_000,
  });
  const scopeMismatch = renderedScope !== scope;
  const zones = useQuery({
    queryKey: ['time-zones', ownerStation(role)],
    queryFn: ({ signal }) => fetchTimeZones(ownerStation(role), signal),
    enabled:
      editable && !capabilityRevoked && accountId !== '' && donationId !== '' && keyId !== '',
    retry: false,
    staleTime: Infinity,
  });
  const authorityRoot = useMemo(
    () => [...recurringLimitsKeys.root(role, accountId), donationId, keyId] as const,
    [accountId, donationId, keyId, role],
  );
  const syncAuthoritativeRead = useCallback(
    async (
      operation: { scope: string; payload: RecurringLimitsWritePayload },
      variables: RecurringLimitsWritePayload,
      forConflict: boolean,
    ): Promise<boolean> => {
      if (
        operationRef.current !== operation ||
        operation.scope !== scope ||
        operation.payload !== variables
      )
        return false;
      if (!forConflict) {
        setAuthoritySyncPending(true);
        setAuthoritySyncUnavailable(false);
      }
      const current = await read.refetch();
      if (
        operationRef.current !== operation ||
        operation.scope !== scope ||
        operation.payload !== variables
      )
        return false;
      if (current.data && !current.error) {
        loadedDataRef.current = current.data;
        setExpectedRevision(current.data.donation_revision);
        if (forConflict) {
          setComparison({ authority: current.data, attempted: variables });
          setComparisonUnavailable(false);
        } else {
          setBaseline(current.data);
          setDraftRules(draftFromResponse(current.data));
          setExpanded(new Set(current.data.rules.map((rule) => rule.id ?? '')));
          setComparison(null);
          setComparisonUnavailable(false);
          setUnknownScope(null);
          setAttemptedPayload(null);
          setAttemptScope(null);
          setAuthoritySyncPending(false);
          setAuthoritySyncUnavailable(false);
          operationRef.current = null;
          setSaveFeedback(true);
        }
        return true;
      }
      if (forConflict) {
        setComparisonUnavailable(true);
      } else {
        setAuthoritySyncPending(false);
        setAuthoritySyncUnavailable(true);
      }
      return false;
    },
    [read, scope],
  );
  const reconcile = useCallback(
    async (variables: RecurringLimitsWritePayload, error: unknown | null) => {
      const operation = operationRef.current;
      if (!operation || operation.scope !== scope || operation.payload !== variables) return;
      if (error instanceof ApiError && error.status === 409) {
        await syncAuthoritativeRead(operation, variables, true);
        return;
      }
      if (error && responseOutcomeUnknown(error)) {
        setUnknownScope(scope);
        return;
      }
      if (!error) {
        await syncAuthoritativeRead(operation, variables, false);
      }
    },
    [scope, syncAuthoritativeRead],
  );
  const save = useRetainedOperation<RecurringLimitsWritePayload, RecurringLimitsWriteReceipt>(
    (payload, idempotencyKey) => {
      if (role === 'owner')
        return Promise.reject(new ApiError('forbidden', 'This action is read-only.', 403));
      return putRecurringLimits(role, donationId, keyId, payload, idempotencyKey);
    },
    reconcile,
    authorityRoot,
  );
  const mutationError = attemptScope === scope ? save.error : null;
  const readCapabilityLost = readOrWriteError(read.error);
  const mutationCapabilityLost = readOrWriteError(mutationError);
  const zonesCapabilityLost = readOrWriteError(zones.error);
  const capabilityLost = readCapabilityLost || mutationCapabilityLost || zonesCapabilityLost;
  const dirty =
    baseline !== null && draftRules !== null
      ? !sameRuleSet(baseline.rules, payloadRulesFromDraft(draftRules))
      : false;
  const currentZones = zones.data?.zones ?? [];
  const validationError = draftRules
    ? draftValidationError(draftRules, zones.data?.zones, copy)
    : undefined;
  const structureMessages =
    baseline && draftRules
      ? structuralChangeMessages(baseline.rules, payloadRulesFromDraft(draftRules), copy)
      : [];
  const unresolvedCommand =
    unknownScope === scope ||
    comparison !== null ||
    comparisonUnavailable ||
    authoritySyncPending ||
    authoritySyncUnavailable;
  const addRule = () => {
    if (!draftRules || draftRules.length >= MAX_RULES || save.isPending || unresolvedCommand)
      return;
    draftID.current += 1;
    const zone = browserTimeZone() ?? 'UTC';
    const item = newDraftRule(`new-${draftID.current}`, zone);
    setDraftRules((current) => (current ? [...current, item] : current));
    setExpanded((current) => new Set(current).add(item.draftId));
    setSaveFeedback(false);
  };
  const updateRule = (draftId: string, update: Partial<RecurringLimitRuleInput>) => {
    if (save.isPending || unresolvedCommand) return;
    setDraftRules(
      (current) =>
        current?.map((rule) => (rule.draftId === draftId ? { ...rule, ...update } : rule)) ??
        current,
    );
    setSaveFeedback(false);
  };
  const removeRule = (draftId: string) => {
    if (save.isPending || unresolvedCommand) return;
    setDraftRules((current) => current?.filter((rule) => rule.draftId !== draftId) ?? current);
    setExpanded((current) => {
      const next = new Set(current);
      next.delete(draftId);
      return next;
    });
    setSaveFeedback(false);
  };
  const moveRule = (draftId: string, direction: -1 | 1) => {
    if (save.isPending || unresolvedCommand) return;
    setDraftRules((current) => {
      if (!current) return current;
      const index = current.findIndex((rule) => rule.draftId === draftId);
      const target = index + direction;
      if (index < 0 || target < 0 || target >= current.length) return current;
      const next = [...current];
      const [item] = next.splice(index, 1);
      if (!item) return current;
      next.splice(target, 0, item);
      return next;
    });
    setSaveFeedback(false);
  };
  const submit = () => {
    if (
      !editable ||
      !draftRules ||
      !baseline ||
      !expectedRevision ||
      save.isPending ||
      validationError ||
      !dirty ||
      unresolvedCommand
    )
      return;
    const payload: RecurringLimitsWritePayload = {
      expected_revision: expectedRevision,
      rules: payloadRulesFromDraft(draftRules),
    };
    setAttemptedPayload(payload);
    setAttemptScope(scope);
    setSavedScope(scope);
    operationRef.current = { scope, payload };
    notifiedReceiptRef.current = null;
    setUnknownScope(null);
    setComparison(null);
    setComparisonUnavailable(false);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    setSaveFeedback(false);
    save.mutate(payload);
  };
  const retryOriginal = () => {
    const operation = operationRef.current;
    if (
      !attemptedPayload ||
      attemptScope !== scope ||
      !operation ||
      operation.scope !== scope ||
      operation.payload !== attemptedPayload ||
      save.isPending
    )
      return;
    setUnknownScope(null);
    save.mutate(attemptedPayload);
  };
  const retryAuthorityRead = async () => {
    const attempted = attemptedPayload;
    const operation = operationRef.current;
    if (
      !attempted ||
      attemptScope !== scope ||
      !operation ||
      operation.scope !== scope ||
      operation.payload !== attempted ||
      save.isPending ||
      read.isFetching
    )
      return;
    await syncAuthoritativeRead(operation, attempted, true);
  };
  const retryAuthoritySync = async () => {
    const attempted = attemptedPayload;
    const operation = operationRef.current;
    if (
      !attempted ||
      attemptScope !== scope ||
      !operation ||
      operation.scope !== scope ||
      operation.payload !== attempted ||
      save.isPending ||
      read.isFetching ||
      !authoritySyncUnavailable
    )
      return;
    await syncAuthoritativeRead(operation, attempted, false);
  };
  const useCurrent = () => {
    if (!comparison) return;
    loadedDataRef.current = comparison.authority;
    setBaseline(comparison.authority);
    setExpectedRevision(comparison.authority.donation_revision);
    setDraftRules(draftFromResponse(comparison.authority));
    setExpanded(new Set(comparison.authority.rules.map((rule) => rule.id ?? '')));
    setComparison(null);
    setAttemptedPayload(null);
    setAttemptScope(null);
    setUnknownScope(null);
    setComparisonUnavailable(false);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    operationRef.current = null;
    notifiedReceiptRef.current = null;
    setSavedScope(null);
    save.reset();
  };
  const keepDraft = () => {
    if (!comparison) return;
    loadedDataRef.current = comparison.authority;
    setBaseline(comparison.authority);
    setExpectedRevision(comparison.authority.donation_revision);
    setComparison(null);
    setComparisonUnavailable(false);
    setAttemptedPayload(null);
    setAttemptScope(null);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    operationRef.current = null;
    notifiedReceiptRef.current = null;
    setSavedScope(null);
    save.reset();
  };
  const discardChanges = () => {
    if (!baseline || save.isPending || unresolvedCommand) return;
    loadedDataRef.current = baseline;
    setDraftRules(draftFromResponse(baseline));
    setExpanded(new Set(baseline.rules.map((rule) => rule.id ?? '')));
    setAttemptedPayload(null);
    setAttemptScope(null);
    setUnknownScope(null);
    setComparison(null);
    setComparisonUnavailable(false);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    setSaveFeedback(false);
    setSavedScope(null);
    operationRef.current = null;
    notifiedReceiptRef.current = null;
    save.reset();
  };

  useEffect(() => {
    if (!scopeChanged(scopeRef.current, identity)) return;
    const previous = scopeRef.current;
    client.removeQueries({
      queryKey: recurringLimitsKeys.detail(
        previous.role,
        previous.accountId,
        previous.donationId,
        previous.keyId,
      ),
      exact: true,
    });
    setRenderedScope(scope);
    scopeRef.current = identity;
    loadedDataRef.current = null;
    setDraftRules(null);
    setBaseline(null);
    setExpectedRevision(null);
    setComparison(null);
    setAttemptedPayload(null);
    setAttemptScope(null);
    setUnknownScope(null);
    setComparisonUnavailable(false);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    setSaveFeedback(false);
    setSavedScope(null);
    setCapabilityRevoked(false);
    setExpanded(new Set());
    operationRef.current = null;
    notifiedReceiptRef.current = null;
    save.reset();
  }, [client, identity, save, scope]);

  useEffect(() => {
    if (!read.data || scopeChanged(scopeRef.current, identity)) return;
    if (
      draftRules === null ||
      (loadedDataRef.current !== read.data &&
        !dirty &&
        comparison === null &&
        !save.isPending &&
        attemptScope === null)
    ) {
      // This effect intentionally snapshots the external authoritative query
      // into the local editor. The dirty guard prevents a background read from
      // replacing an in-progress draft.
      loadedDataRef.current = read.data;
      setBaseline(read.data);
      setExpectedRevision(read.data.donation_revision);
      setDraftRules(draftFromResponse(read.data));
      setExpanded(new Set(read.data.rules.map((rule) => rule.id ?? '')));
    }
  }, [attemptScope, comparison, dirty, draftRules, identity, read.data, save.isPending]);

  useEffect(() => {
    if (!capabilityLost || capabilityRevoked) return;
    // Stop observers before evicting the station cache after an authority loss.
    // This state transition is intentionally driven by the external auth result.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCapabilityRevoked(true);
    clearStationSession(client, authorityFrame(role));
    void client.cancelQueries({ queryKey: recurringLimitsKeys.root(role, accountId) });
    client.removeQueries({ queryKey: recurringLimitsKeys.root(role, accountId) });
    loadedDataRef.current = null;
    setDraftRules(null);
    setBaseline(null);
    setExpectedRevision(null);
    setComparison(null);
    setAttemptedPayload(null);
    setAttemptScope(null);
    setUnknownScope(null);
    setComparisonUnavailable(false);
    setAuthoritySyncPending(false);
    setAuthoritySyncUnavailable(false);
    setSaveFeedback(false);
    setSavedScope(null);
    setExpanded(new Set());
    operationRef.current = null;
    notifiedReceiptRef.current = null;
    onCapabilityLoss?.();
  }, [accountId, capabilityLost, capabilityRevoked, client, onCapabilityLoss, role]);

  useEffect(() => {
    if (!save.data || scopeMismatch || savedScope !== scope) return;
    if (notifiedReceiptRef.current === save.data) return;
    notifiedReceiptRef.current = save.data;
    setSaveFeedback(true);
    onSaved?.(save.data);
  }, [onSaved, save.data, savedScope, scope, scopeMismatch]);

  if (capabilityLost || capabilityRevoked) {
    return (
      <p className="field-error recurring-limits__access-lost" role="alert">
        {copy.accessLost}
      </p>
    );
  }
  if (read.error && baseline === null)
    return <ErrorState error={read.error} onRetry={() => void read.refetch()} />;
  if (scopeMismatch || read.isPending || draftRules === null) return <LoadingState />;
  const viewByID = new Map(read.data?.rules.map((rule) => [rule.id, rule]) ?? []);
  const serverNow = read.data?.server_now ?? baseline?.server_now ?? 0;
  const browserZone = browserTimeZone() ?? 'UTC';
  const attempted = comparison?.attempted.rules ?? attemptedPayload?.rules ?? [];
  return (
    <section className="recurring-limits" aria-label={copy.title}>
      <header className="recurring-limits__header">
        <div>
          <h3>{copy.title}</h3>
          <p>{copy.description}</p>
        </div>
        <p className="recurring-limits__account-scope">{copy.browserZone(browserZone)}</p>
      </header>
      {role === 'owner' ? (
        <p className="recurring-limits__notice">{copy.readOnly}</p>
      ) : readOnly ? (
        <p className="recurring-limits__notice">{copy.readOnlyRestricted}</p>
      ) : null}
      <details className="recurring-limits__help">
        <summary>{copy.helpTitle}</summary>
        <p>{copy.charityOnly}</p>
        <p>{copy.privacyNote}</p>
      </details>
      <div className="recurring-limits__toolbar">
        <span>{copy.ruleCount(draftRules.length)}</span>
        {editable ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={save.isPending || unresolvedCommand || draftRules.length >= MAX_RULES}
            onClick={addRule}
          >
            {copy.addRule}
          </button>
        ) : null}
      </div>
      {draftRules.length >= MAX_RULES ? (
        <p className="recurring-limits__limit-note">{copy.maxRules}</p>
      ) : null}
      {draftRules.length === 0 ? <p className="recurring-limits__empty">{copy.noRules}</p> : null}
      <div className="recurring-limits__rules">
        {draftRules.map((rule, index) => (
          <RuleCard
            key={rule.draftId}
            copy={copy}
            draft={rule}
            index={index}
            total={draftRules.length}
            view={rule.id ? viewByID.get(rule.id) : undefined}
            editable={editable}
            disabled={save.isPending || unresolvedCommand}
            expanded={expanded.has(rule.draftId)}
            zones={currentZones}
            serverNow={serverNow}
            locale={locale}
            onToggle={() =>
              setExpanded((current) => {
                const next = new Set(current);
                if (next.has(rule.draftId)) next.delete(rule.draftId);
                else next.add(rule.draftId);
                return next;
              })
            }
            onChange={(update) => updateRule(rule.draftId, update)}
            onDelete={() => removeRule(rule.draftId)}
            onMove={(direction) => moveRule(rule.draftId, direction)}
          />
        ))}
      </div>
      {editable ? (
        <form
          className="recurring-limits__form"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          {zones.error ? (
            <div role="alert">
              <p className="field-error">{copy.timeZoneUnavailable}</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={zones.isFetching}
                onClick={() => void zones.refetch()}
              >
                {copy.retryTimeZones}
              </button>
            </div>
          ) : null}
          {validationError ? (
            <p className="field-error" role="alert">
              {validationError}
            </p>
          ) : null}
          {structureMessages.length > 0 ? (
            <aside className="recurring-limits__changes" aria-label={copy.structuralChanges}>
              <strong>{copy.structuralChanges}</strong>
              <ul>
                {structureMessages.map((message) => (
                  <li key={message}>{message}</li>
                ))}
              </ul>
              <p>{copy.preserveUsage}</p>
            </aside>
          ) : null}
          {comparison ? (
            <ConflictPanel
              copy={copy}
              comparison={comparison}
              attempted={attempted}
              onUseCurrent={useCurrent}
              onKeepDraft={keepDraft}
            />
          ) : null}
          {comparisonUnavailable ? (
            <aside className="recurring-limits__unknown" role="alert">
              <strong>{copy.conflictTitle}</strong>
              <p>{copy.authorityUnavailable}</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={save.isPending || read.isFetching}
                onClick={() => void retryAuthorityRead()}
              >
                {copy.retryAuthority}
              </button>
            </aside>
          ) : null}
          {authoritySyncPending ? (
            <p className="recurring-limits__saved" role="status">
              {copy.authoritySyncPending}
            </p>
          ) : null}
          {authoritySyncUnavailable ? (
            <aside className="recurring-limits__unknown" role="alert">
              <strong>{copy.authoritySyncTitle}</strong>
              <p>{copy.authoritySyncUnavailable}</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={save.isPending || read.isFetching || authoritySyncPending}
                onClick={() => void retryAuthoritySync()}
              >
                {copy.retryAuthority}
              </button>
            </aside>
          ) : null}
          {unknownScope === scope ? (
            <aside className="recurring-limits__unknown" role="alert">
              <strong>{copy.unknownTitle}</strong>
              <p>{copy.unknownBody}</p>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={save.isPending}
                onClick={retryOriginal}
              >
                {copy.retryOriginal}
              </button>
            </aside>
          ) : null}
          {mutationError &&
          !responseOutcomeUnknown(mutationError) &&
          !(mutationError instanceof ApiError && mutationError.status === 409) &&
          !mutationCapabilityLost ? (
            <ErrorState error={mutationError} />
          ) : null}
          {saveFeedback ? (
            <p className="recurring-limits__saved" role="status">
              {copy.saveSuccess}
            </p>
          ) : null}
          <div className="recurring-limits__save-row">
            <button
              type="submit"
              className="btn btn-primary"
              disabled={
                save.isPending ||
                !dirty ||
                Boolean(validationError) ||
                unresolvedCommand ||
                zones.isPending ||
                Boolean(zones.error) ||
                !expectedRevision
              }
            >
              {save.isPending ? copy.saving : copy.save}
            </button>
            {dirty ? (
              <button
                type="button"
                className="btn btn-quiet"
                disabled={save.isPending || unresolvedCommand}
                onClick={discardChanges}
              >
                {copy.discardChanges}
              </button>
            ) : null}
            {!dirty && !save.isPending ? (
              <span className="recurring-limits__hint">{copy.noChanges}</span>
            ) : null}
          </div>
        </form>
      ) : null}
    </section>
  );
}

export type { DraftRule, RecurringLimitsProps };
