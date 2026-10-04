import { useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import {
  Affix,
  Field,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
  Toggle,
} from '@shared/components/ui';
import { usePictureBookText } from '@shared/picturebook/copy';
import { getLakeConfig } from '@shared/lakenotes/api';
import { decoded } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  getAdminConfig,
  decodeDetail,
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
export type PictureBookSection = 'exchange' | 'service' | 'models' | 'recovery';
function ConfigForm({ detail }: { readonly detail: ActivityDetail }) {
  const copy = usePictureBookText();
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
    <Panel>
      <PanelHead title={text('common.availabilityAndExchangeSettings')} />
      <PanelBody>
        <Note>{text('common.hidingRemovesTheDirectoryEntryDirectLinks')}</Note>
        <form
          className="limited-form"
          onChange={() => {
            setSaved(false);
            setFormError(null);
          }}
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <fieldset disabled={locked}>
            <div className="nb-grid nb-grid--2">
              <Toggle
                label={text('common.showInActivityDirectory')}
                checked={visible}
                disabled={locked}
                onChange={setVisible}
              />
              <Toggle
                label={text('common.pauseActivity')}
                checked={paused}
                disabled={locked}
                onChange={setPaused}
              />
            </div>
            <TimeContextNotice station="admin" />
            <div className="nb-grid nb-grid--2">
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
            <div className="nb-grid nb-grid--3">
              <Field label={text('common.generalCreditsPerSheet')}>
                {(props) => (
                  <Affix
                    {...props}
                    unit={copy('积分', 'credits')}
                    inputMode="decimal"
                    value={paper}
                    onChange={(event) => setPaper(event.target.value)}
                    required
                    maxLength={20}
                  />
                )}
              </Field>
              <Field label={text('common.generalCreditsPerBrush')}>
                {(props) => (
                  <Affix
                    {...props}
                    unit={copy('积分', 'credits')}
                    inputMode="decimal"
                    value={brush}
                    onChange={(event) => setBrush(event.target.value)}
                    required
                    maxLength={20}
                  />
                )}
              </Field>
              <Field label={text('common.totalCumulativeBrushExchangeCap')}>
                {(props) => (
                  <Affix
                    {...props}
                    unit={copy('支', 'brushes')}
                    inputMode="numeric"
                    value={cap}
                    onChange={(event) => setCap(event.target.value)}
                    required
                    maxLength={39}
                  />
                )}
              </Field>
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
      </PanelBody>
    </Panel>
  );
}
export function LimitedActivitiesPage({
  children,
}: {
  readonly children?: ReactNode | ((section: PictureBookSection) => ReactNode);
}) {
  const [params] = useSearchParams();
  const pictureBook = params.get('activity') === 'picture-book';
  const [section, setSection] = useState<PictureBookSection>('exchange');
  const copy = usePictureBookText();
  const { t: lakeText } = useLakeAdminCopy();
  const text = useActivityText(),
    session = useAdminSession();
  const query = useQuery({
    queryKey: configKey,
    queryFn: getAdminConfig,
    enabled: !!session.data?.admin && !session.error && !session.isFetching,
    refetchOnWindowFocus: false,
  });
  const enabled = !!session.data?.admin && !session.error && !session.isFetching && !pictureBook;
  const lake = useQuery({
    queryKey: ['admin', 'lake-notes', 'config'],
    queryFn: () => getLakeConfig(),
    enabled,
    refetchOnWindowFocus: false,
  });
  const fish = useQuery({
    queryKey: ['admin', 'limited-activities', 'fat-fish'],
    queryFn: () => decoded('/admin/api/limited-activities/fat-fish', decodeDetail),
    enabled,
    refetchOnWindowFocus: false,
  });
  const tabs = [
    { value: 'exchange' as const, label: copy('开放与兑换', 'Availability and exchange') },
    { value: 'service' as const, label: copy('图片生成服务', 'Image generation service') },
    { value: 'models' as const, label: copy('模型目录', 'Model catalog') },
    { value: 'recovery' as const, label: copy('保护与恢复', 'Protection and recovery') },
  ].map((tab) => ({
    ...tab,
    id: `picture-book-tab-${tab.value}`,
    panelId: `picture-book-panel-${tab.value}`,
  }));
  const status = (value: typeof query | typeof lake | typeof fish) =>
    value.isPending ? (
      <LoadingState />
    ) : value.error ? (
      <ErrorState error={value.error} onRetry={() => void value.refetch()} />
    ) : value.data ? (
      <StatusBadge
        active={value.data.status === 'open'}
        label={statusLabel(value.data.status, text)}
      />
    ) : null;
  return (
    <div className="page">
      <PageHeader
        title={
          pictureBook
            ? copy('喵帕斯的绘本', 'Nyanpasu’s picture book')
            : text('common.limitedTimeActivities')
        }
        description={
          pictureBook
            ? text('common.configureThePictureBookScheduleAndCurrency')
            : copy('管理活动的开放时间与玩法。', 'Manage activity schedules and settings.')
        }
        back={
          pictureBook ? (
            <Link to="/limited-activities">
              {copy('返回限时活动', 'Back to limited activities')}
            </Link>
          ) : undefined
        }
        icon="activities"
      />
      {session.data?.admin && !session.error && !pictureBook ? (
        <div className="nb-grid nb-grid--3">
          {[
            {
              title: copy('喵帕斯的绘本', 'Nyanpasu’s picture book'),
              description: copy(
                '生成图片，收集你的绘本。',
                'Generate images and build a picture book.',
              ),
              to: '/limited-activities?activity=picture-book',
              query,
            },
            {
              title: lakeText('title'),
              description: lakeText('description'),
              to: '/limited-activities/lake-notes',
              query: lake,
            },
            {
              title: text('common.fatFish'),
              description: copy('编辑关卡并安排开放期次。', 'Edit levels and schedule periods.'),
              to: '/limited-activities/fat-fish',
              query: fish,
            },
          ].map((entry) => (
            <Panel key={entry.to}>
              <PanelHead title={entry.title} />
              <PanelBody>
                <div className="nb-stack">
                  {status(entry.query)}
                  <p>{entry.description}</p>
                  <Link className="btn btn-secondary" to={entry.to}>
                    {copy('设置', 'Settings')}
                  </Link>
                </div>
              </PanelBody>
            </Panel>
          ))}
        </div>
      ) : null}
      {session.isPending ? (
        <LoadingState />
      ) : session.error ? (
        <ErrorState error={session.error} />
      ) : !pictureBook ? null : query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <Tabs
            label={copy('绘本设置', 'Picture book settings')}
            value={section}
            tabs={tabs}
            onChange={setSection}
          />
          <div
            id="picture-book-panel-exchange"
            role="tabpanel"
            aria-labelledby="picture-book-tab-exchange"
            hidden={section !== 'exchange'}
          >
            <ConfigForm detail={query.data} />
          </div>
          {typeof children === 'function' ? children(section) : children}
        </>
      ) : null}
    </div>
  );
}
