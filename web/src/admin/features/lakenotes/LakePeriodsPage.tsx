import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  getLakeConfig,
  getPeriods,
  savePeriod,
  updateLakeConfig,
  directions,
  directionUnits,
  naturalToUnits,
  unitsToNatural,
  type LakeDirectory,
  type Period,
  type PeriodInput,
  type Direction,
  type LakeConfigInput,
} from '@shared/lakenotes/api';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../../data';
import { useLakeAdminCopy, type LakeText } from './copy';
import './periods.css';

const root = ['admin', 'lake-notes'] as const;
function Availability({ detail, text }: { detail: LakeDirectory; text: LakeText }) {
  const client = useQueryClient(),
    [revision, setRevision] = useState(detail.revision),
    [visible, setVisible] = useState(detail.visible),
    [paused, setPaused] = useState(detail.paused),
    [start, setStart] = useState(() => createTimeDraft(detail.starts_at, 'second')),
    [end, setEnd] = useState(() => createTimeDraft(detail.ends_at, 'second')),
    [error, setError] = useState<unknown>(null);
  const operation = useRetainedOperation(
    async (input: LakeConfigInput, key, context) => {
      const value = await updateLakeConfig(input, key, { signal: context.signal });
      context.commit(() => {
        setRevision(value.revision);
        client.setQueryData([...root, 'config'], value);
      });
      return value;
    },
    () => undefined,
    root,
  );
  const unknown = operation.outcome === 'unknown',
    locked = unknown || operation.isPending;
  const submit = () => {
    if (unknown && operation.variables) {
      operation.mutate(operation.variables);
      return;
    }
    setError(null);
    const starts = timeDraftValue(start),
      ends = timeDraftValue(end);
    if (
      starts === undefined ||
      ends === undefined ||
      (starts === null) !== (ends === null) ||
      (starts !== null && ends !== null && starts >= ends)
    ) {
      setError(new Error(text('invalid')));
      return;
    }
    operation.mutate({
      expected_revision: revision,
      visible,
      paused,
      starts_at: starts,
      ends_at: ends,
      module_config: {},
    });
  };
  return (
    <Card>
      <h2>{text('availability')}</h2>
      <p>{text('availabilityHelp')}</p>
      <form
        className="lake-period-form"
        onChange={() => {
          operation.reset();
          setError(null);
        }}
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset disabled={locked}>
          <label className="lake-period-check">
            <input
              type="checkbox"
              checked={visible}
              onChange={(event) => setVisible(event.target.checked)}
            />
            {text('visible')}
          </label>
          <label className="lake-period-check">
            <input
              type="checkbox"
              checked={paused}
              onChange={(event) => setPaused(event.target.checked)}
            />
            {text('paused')}
          </label>
          <TimeContextNotice station="admin" />
          <div className="lake-period-grid">
            <TimeInput
              station="admin"
              showZoneHint={false}
              label={text('start')}
              draft={start}
              onChange={setStart}
            />
            <TimeInput
              station="admin"
              showZoneHint={false}
              label={text('end')}
              draft={end}
              onChange={setEnd}
            />
          </div>
          <p>{text('umbrellaHelp')}</p>
        </fieldset>
        {error || operation.error ? <ErrorState error={error ?? operation.error} /> : null}
        {unknown ? (
          <p role="status">{text('unknown')}</p>
        ) : operation.isSuccess ? (
          <p role="status">{text('saved')}</p>
        ) : null}
        <button
          className="btn btn-primary"
          type="submit"
          disabled={
            operation.isPending ||
            (!unknown && (timeDraftValue(start) === undefined || timeDraftValue(end) === undefined))
          }
        >
          {unknown ? text('retry') : text('saveAvailability')}
        </button>
      </form>
    </Card>
  );
}
function PeriodEditor({
  period,
  text,
  onClose,
  onSaved,
  onEdit,
}: {
  period: Period | null;
  text: LakeText;
  onClose: () => void;
  onSaved: (value: Period) => void;
  onEdit: () => void;
}) {
  const client = useQueryClient(),
    [name, setName] = useState(period?.name ?? ''),
    [status, setStatus] = useState<Period['status']>(period?.status ?? 'draft'),
    [start, setStart] = useState(() => createTimeDraft(period?.starts_at ?? null, 'second')),
    [end, setEnd] = useState(() => createTimeDraft(period?.ends_at ?? null, 'second')),
    [fee, setFee] = useState(
      period?.entry_fee_milli === null || !period
        ? ''
        : unitsToNatural(period.entry_fee_milli, 'general'),
    ),
    [error, setError] = useState<unknown>(null);
  const [settings, setSettings] = useState(
    () =>
      Object.fromEntries(
        directions.map((direction) => {
          const setting = period?.exchanges[direction],
            pair = directionUnits[direction];
          return [
            direction,
            {
              enabled: setting?.enabled ?? false,
              source: setting?.source_amount ? unitsToNatural(setting.source_amount, pair[0]) : '',
              target: setting?.target_amount ? unitsToNatural(setting.target_amount, pair[1]) : '',
            },
          ];
        }),
      ) as Record<Direction, { enabled: boolean; source: string; target: string }>,
  );
  const operation = useRetainedOperation(
    async (input: PeriodInput, key, context) => {
      const value = await savePeriod(period?.id ?? null, input, key, { signal: context.signal });
      await context.commit(async () => {
        await Promise.all([
          client.invalidateQueries({ queryKey: root }),
          client.invalidateQueries({ queryKey: ['user', 'lake-notes'] }),
        ]);
      });
      context.commit(() => onSaved(value));
      return value;
    },
    () => undefined,
    root,
  );
  const unknown = operation.outcome === 'unknown',
    locked = unknown || operation.isPending;
  const submit = () => {
    if (unknown && operation.variables) {
      operation.mutate(operation.variables);
      return;
    }
    setError(null);
    try {
      const starts = timeDraftValue(start),
        ends = timeDraftValue(end);
      if (
        starts === undefined ||
        starts === null ||
        ends === undefined ||
        ends === null ||
        starts >= ends ||
        !name.trim() ||
        Array.from(name).length > 80
      )
        throw new Error(text('invalid'));
      const entryFee = fee === '' ? null : naturalToUnits(fee, 'general', true);
      if (
        (status === 'published' && entryFee === null) ||
        (entryFee !== null && BigInt(entryFee) > 9000000000000000n)
      )
        throw new Error(text('invalid'));
      const exchanges = Object.fromEntries(
        directions.map((direction) => {
          const setting = settings[direction],
            pair = directionUnits[direction];
          if (!setting.enabled && setting.source === '' && setting.target === '')
            return [direction, { enabled: false, source_amount: '', target_amount: '' }];
          return [
            direction,
            {
              enabled: setting.enabled,
              source_amount: naturalToUnits(setting.source, pair[0]),
              target_amount: naturalToUnits(setting.target, pair[1]),
            },
          ];
        }),
      ) as PeriodInput['exchanges'];
      operation.mutate({
        expected_revision: period?.revision ?? '0',
        name,
        status,
        starts_at: starts,
        ends_at: ends,
        entry_fee_milli: entryFee,
        exchanges,
      });
    } catch (e) {
      setError(e);
    }
  };
  const change = (
    direction: Direction,
    field: 'enabled' | 'source' | 'target',
    value: string | boolean,
  ) => setSettings((old) => ({ ...old, [direction]: { ...old[direction], [field]: value } }));
  return (
    <Card className="lake-period-editor">
      <h2>{period ? text('edit') : text('newPeriod')}</h2>
      <p>{text('periodHelp')}</p>
      <form
        className="lake-period-form"
        onChange={() => {
          operation.reset();
          setError(null);
          onEdit();
        }}
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset disabled={locked}>
          <div className="lake-period-grid">
            <label>
              {text('name')}
              <input
                maxLength={160}
                required
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label>
              {text('status')}
              <select
                aria-label={text('status')}
                value={status}
                onChange={(event) => setStatus(event.target.value as Period['status'])}
              >
                {(['draft', 'published', 'cancelled'] as const).map((value) => (
                  <option key={value} value={value}>
                    {text(value)}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <TimeContextNotice station="admin" />
          <div className="lake-period-grid">
            <TimeInput
              station="admin"
              showZoneHint={false}
              label={text('start')}
              draft={start}
              onChange={setStart}
            />
            <TimeInput
              station="admin"
              showZoneHint={false}
              label={text('end')}
              draft={end}
              onChange={setEnd}
            />
          </div>
          <label>
            {text('fee')}
            <input
              inputMode="decimal"
              value={fee}
              maxLength={39}
              onChange={(event) => setFee(event.target.value)}
            />
          </label>
          <p>{text('feeHelp')}</p>
          {status === 'cancelled' ? <p>{text('cancelHelp')}</p> : null}
          <h3>{text('exchangeHeading')}</h3>
          <p>{text('exchangeHelp')}</p>
          <div className="lake-period-grid">
            {directions.map((direction) => {
              const setting = settings[direction],
                pair = directionUnits[direction];
              return (
                <section className="lake-exchange-setting" key={direction}>
                  <h4>{text(direction)}</h4>
                  <label className="lake-period-check">
                    <input
                      type="checkbox"
                      checked={setting.enabled}
                      onChange={(event) => change(direction, 'enabled', event.target.checked)}
                    />
                    {text('enabled')}
                  </label>
                  <label>
                    {text('source')} · {text(pair[0])}
                    <input
                      inputMode={pair[0] === 'coins' ? 'numeric' : 'decimal'}
                      maxLength={42}
                      value={setting.source}
                      onChange={(event) => change(direction, 'source', event.target.value)}
                      required={setting.enabled}
                    />
                  </label>
                  <label>
                    {text('target')} · {text(pair[1])}
                    <input
                      inputMode={pair[1] === 'coins' ? 'numeric' : 'decimal'}
                      maxLength={42}
                      value={setting.target}
                      onChange={(event) => change(direction, 'target', event.target.value)}
                      required={setting.enabled}
                    />
                  </label>
                </section>
              );
            })}
          </div>
        </fieldset>
        {error || operation.error ? <ErrorState error={error ?? operation.error} /> : null}
        {unknown ? (
          <p role="status">{text('unknown')}</p>
        ) : operation.isSuccess ? (
          <p role="status">{text('saved')}</p>
        ) : null}
        <div className="lake-period-actions">
          <button
            type="submit"
            className="btn btn-primary"
            disabled={
              operation.isPending ||
              (!unknown &&
                (timeDraftValue(start) === undefined || timeDraftValue(end) === undefined))
            }
          >
            {unknown
              ? text('retry')
              : status === 'published'
                ? text('publish')
                : status === 'draft'
                  ? text('saveDraft')
                  : text('savePeriod')}
          </button>
          <button type="button" className="btn btn-secondary" disabled={locked} onClick={onClose}>
            {text('cancelEdit')}
          </button>
        </div>
      </form>
    </Card>
  );
}
export function LakePeriodsPage() {
  const formatDateTime = useDateTimeFormatter();
  const { t: text } = useLakeAdminCopy(),
    session = useAdminSession(),
    [page, setPage] = useState(1),
    [editor, setEditor] = useState<Period | null | undefined>(undefined),
    [saved, setSaved] = useState(false);
  const enabled = Boolean(session.data?.admin && !session.error && !session.isFetching);
  const config = useQuery({
    queryKey: [...root, 'config'],
    queryFn: ({ signal }) => getLakeConfig({ signal }),
    enabled,
    refetchOnWindowFocus: false,
  });
  const periods = useQuery({
    queryKey: [...root, 'periods', page],
    queryFn: ({ signal }) => getPeriods(page, { signal }),
    enabled,
    refetchOnWindowFocus: false,
  });
  return (
    <div className="page">
      <PageHeader
        title={text('title')}
        description={text('description')}
        icon="activities"
        back={<Link to="/limited-activities">{text('back')}</Link>}
      />
      {session.isPending ? (
        <LoadingState />
      ) : session.error ? (
        <ErrorState error={session.error} />
      ) : !session.data?.admin ? null : (
        <>
          {config.isPending ? (
            <LoadingState />
          ) : config.error ? (
            <ErrorState error={config.error} onRetry={() => void config.refetch()} />
          ) : config.data ? (
            <Availability detail={config.data} text={text} />
          ) : null}
          <Card>
            <div className="lake-period-heading">
              <h2>{text('periods')}</h2>
              <button
                className="btn btn-primary"
                type="button"
                disabled={editor !== undefined}
                onClick={() => {
                  setSaved(false);
                  setEditor(null);
                }}
              >
                {text('newPeriod')}
              </button>
            </div>
            {periods.isPending ? (
              <LoadingState />
            ) : periods.error ? (
              <ErrorState error={periods.error} onRetry={() => void periods.refetch()} />
            ) : periods.data ? (
              <>
                {!periods.data.items.length ? <p>{text('empty')}</p> : null}
                <div className="lake-period-list">
                  {periods.data.items.map((period) => (
                    <article key={period.id}>
                      <h3>{period.name}</h3>
                      <p>
                        {text(period.status)} · {formatDateTime(period.starts_at)} —{' '}
                        {formatDateTime(period.ends_at)}
                      </p>
                      <p>
                        {text('fee')}:{' '}
                        {period.entry_fee_milli === null
                          ? '—'
                          : unitsToNatural(period.entry_fee_milli, 'general')}
                      </p>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        disabled={editor !== undefined}
                        onClick={() => {
                          setSaved(false);
                          setEditor(period);
                        }}
                      >
                        {text('edit')}
                      </button>
                    </article>
                  ))}
                </div>
                <div className="lake-period-actions">
                  <button
                    type="button"
                    className="btn btn-secondary"
                    disabled={page === 1}
                    onClick={() => setPage((n) => n - 1)}
                  >
                    {text('previous')}
                  </button>
                  <span>{page}</span>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    disabled={!periods.data.has_more}
                    onClick={() => setPage((n) => n + 1)}
                  >
                    {text('next')}
                  </button>
                </div>
              </>
            ) : null}
          </Card>
          {saved ? <p role="status">{text('saved')}</p> : null}
          {editor !== undefined ? (
            <PeriodEditor
              key={(editor?.id ?? 'new') + ':' + (editor?.revision ?? '0')}
              period={editor}
              text={text}
              onClose={() => setEditor(undefined)}
              onEdit={() => setSaved(false)}
              onSaved={(value) => {
                setEditor(value);
                setSaved(true);
              }}
            />
          ) : null}
        </>
      )}
    </div>
  );
}
