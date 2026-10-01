import { useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  getAdminConfig,
  updateConfig,
  type ActivityDetail,
  type ActivityConfigInput,
} from '@shared/limitedactivities/api';
import { statusLabel, useActivityText } from '@shared/limitedactivities/copy';
import { ApiError } from '@shared/query/http';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useLakeAdminCopy } from '../features/lakenotes/copy';
import { useAdminSession } from '../data';
import '@shared/limitedactivities/limited.css';
const configKey = ['admin', 'limited-activities', 'picture-book'] as const;
function ConfigForm({ detail }: { readonly detail: ActivityDetail }) {
  const text = useActivityText(),
    client = useQueryClient();
  const [visible, setVisible] = useState(detail.visible),
    [paused, setPaused] = useState(detail.paused),
    [start, setStart] = useState(() => createTimeDraft(detail.starts_at, 'second')),
    [end, setEnd] = useState(() => createTimeDraft(detail.ends_at, 'second'));
  const [paper, setPaper] = useState(detail.module_config.paper_price),
    [brush, setBrush] = useState(detail.module_config.brush_price),
    [cap, setCap] = useState(detail.module_config.brush_cap),
    [formError, setFormError] = useState<unknown>(null);
  const [saved, setSaved] = useState(false);
  const save = useRetainedOperation(updateConfig, () => undefined, ['admin', 'limited-activities']);
  const uncertain = save.isError && responseOutcomeUnknown(save.error),
    locked = save.isPending || uncertain;
  const submit = () => {
    if (save.isPending) return;
    setSaved(false);
    setFormError(null);
    try {
      let input: ActivityConfigInput;
      if (uncertain && save.variables) input = save.variables;
      else {
        const starts_at = timeDraftValue(start),
          ends_at = timeDraftValue(end);
        if (
          starts_at === undefined ||
          ends_at === undefined ||
          (starts_at === null) !== (ends_at === null) ||
          (starts_at !== null && ends_at !== null && starts_at >= ends_at)
        )
          throw new ApiError(
            'invalid_request',
            'Set both times in the site time zone, with the end after the start.',
            400,
          );
        input = {
          expected_revision: detail.revision,
          visible,
          paused,
          starts_at,
          ends_at,
          module_config: { paper_price: paper, brush_price: brush, brush_cap: cap },
        };
      }
      save.mutate(input, {
        onSuccess: (value) => {
          client.setQueryData(configKey, value);
          setSaved(true);
        },
      });
    } catch (error) {
      setFormError(error);
    }
  };
  return (
    <Card>
      <h2>{text('common.availabilityAndExchangeSettings')}</h2>
      <p role="status">{statusLabel(detail.status, text)}</p>
      <p>{text('common.hidingRemovesTheDirectoryEntryDirectLinks')}</p>
      <form
        className="limited-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset disabled={locked}>
          <label className="limited-checkbox">
            <input
              type="checkbox"
              checked={visible}
              onChange={(event) => setVisible(event.target.checked)}
            />
            {text('common.showInActivityDirectory')}
          </label>
          <label className="limited-checkbox">
            <input
              type="checkbox"
              checked={paused}
              onChange={(event) => setPaused(event.target.checked)}
            />
            {text('common.pauseActivity')}
          </label>
          <TimeContextNotice station="admin" />
          <div className="limited-grid">
            <TimeInput
              label={text('common.openingTime')}
              station="admin"
              draft={start}
              showZoneHint={false}
              onChange={setStart}
            />
            <TimeInput
              label={text('common.closingTimeExclusive')}
              station="admin"
              draft={end}
              showZoneHint={false}
              onChange={setEnd}
            />
          </div>
          <p>{text('common.leaveBothTimesEmptyToKeepThe')}</p>
          <div className="limited-grid">
            <label>
              {text('common.generalCreditsPerSheet')}
              <input
                inputMode="decimal"
                value={paper}
                onChange={(event) => setPaper(event.target.value)}
                required
                maxLength={20}
              />
            </label>
            <label>
              {text('common.generalCreditsPerBrush')}
              <input
                inputMode="decimal"
                value={brush}
                onChange={(event) => setBrush(event.target.value)}
                required
                maxLength={20}
              />
            </label>
            <label>
              {text('common.totalCumulativeBrushExchangeCap')}
              <input
                inputMode="numeric"
                value={cap}
                onChange={(event) => setCap(event.target.value)}
                required
                maxLength={39}
              />
            </label>
          </div>
        </fieldset>
        <p>
          {text('common.brushesAlreadyExchanged')}: {detail.module_config.brush_exchanged} ·{' '}
          {text('common.remaining')}: {detail.module_config.brush_remaining}
        </p>
        <p>{text('common.aCapBelowTheExchangedTotalStops')}</p>
        {uncertain ? (
          <p role="status">{text('common.theSaveResultIsUnconfirmedRetryThe')}</p>
        ) : null}
        {formError || save.error ? <ErrorState error={formError ?? save.error} /> : null}
        {saved ? <p role="status">{text('common.settingsSaved')}</p> : null}
        <button className="btn btn-primary" type="submit" disabled={save.isPending}>
          {uncertain ? text('common.retrySave') : text('common.saveSettings')}
        </button>
      </form>
    </Card>
  );
}
export function LimitedActivitiesPage({ children }: { readonly children?: ReactNode }) {
  const { t: lakeText } = useLakeAdminCopy();
  const text = useActivityText(),
    session = useAdminSession();
  const query = useQuery({
    queryKey: configKey,
    queryFn: getAdminConfig,
    enabled: !!session.data?.admin && !session.error && !session.isFetching,
    refetchOnWindowFocus: false,
  });
  return (
    <div className="page">
      <PageHeader
        title={text('common.limitedTimeActivities')}
        description={text('common.configureThePictureBookScheduleAndCurrency')}
        icon="activities"
      />
      {session.data?.admin ? (
        <Card>
          <h2>{lakeText('title')}</h2>
          <p>{lakeText('description')}</p>
          <Link className="btn btn-primary" to="/limited-activities/lake-notes">
            {lakeText('periods')}
          </Link>
        </Card>
      ) : null}
      {session.data?.admin ? (
        <Card>
          <h2>{text('common.fatFish')}</h2>
          <p>{text('common.manageLevelDraftsImmutableVersionsPlaytestsAnd')}</p>
          <Link className="btn btn-primary" to="/limited-activities/fat-fish">
            {text('common.openLevelAndPeriodEditor')}
          </Link>
        </Card>
      ) : null}
      {session.isPending ? (
        <LoadingState />
      ) : session.error ? (
        <ErrorState error={session.error} />
      ) : query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <ConfigForm detail={query.data} />
          {children}
        </>
      ) : null}
    </div>
  );
}
