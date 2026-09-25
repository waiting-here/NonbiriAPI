import { useState, type ReactNode } from 'react';
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
import { useAdminSession } from '../data';
import '@shared/limitedactivities/limited.css';

const configKey = ['admin', 'limited-activities', 'picture-book'] as const;
function ConfigForm({ detail }: { readonly detail: ActivityDetail }) {
  const t = useActivityText(),
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
      <h2>{t('开放与兑换配置', 'Availability and exchange settings')}</h2>
      <p role="status">{statusLabel(detail.status, t)}</p>
      <p>
        {t(
          '隐藏只移除活动列表入口，知道链接的用户仍可访问。活动暂停或站点维护会取消未派发任务并退款。',
          'Hiding removes the directory entry; direct links remain accessible. Pausing or site maintenance cancels queued tasks and refunds their charges.',
        )}
      </p>
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
            {t('显示活动入口', 'Show in activity directory')}
          </label>
          <label className="limited-checkbox">
            <input
              type="checkbox"
              checked={paused}
              onChange={(event) => setPaused(event.target.checked)}
            />
            {t('暂停活动', 'Pause activity')}
          </label>
          <TimeContextNotice station="admin" />
          <div className="limited-grid">
            <TimeInput
              label={t('开启时间', 'Opening time')}
              station="admin"
              draft={start}
              showZoneHint={false}
              onChange={setStart}
            />
            <TimeInput
              label={t('结束时间（不含此时刻）', 'Closing time (exclusive)')}
              station="admin"
              draft={end}
              showZoneHint={false}
              onChange={setEnd}
            />
          </div>
          <p>
            {t(
              '两个时间均留空表示未开启。再次开放会保留余额、记录与累计兑换量。',
              'Leave both times empty to keep the activity unconfigured. Reopening preserves balances, records and cumulative exchanges.',
            )}
          </p>
          <div className="limited-grid">
            <label>
              {t('每张草稿纸所需通用积分', 'General credits per sheet')}
              <input
                inputMode="decimal"
                value={paper}
                onChange={(event) => setPaper(event.target.value)}
                required
                maxLength={20}
              />
            </label>
            <label>
              {t('每支画笔所需通用积分', 'General credits per brush')}
              <input
                inputMode="decimal"
                value={brush}
                onChange={(event) => setBrush(event.target.value)}
                required
                maxLength={20}
              />
            </label>
            <label>
              {t('画笔累计兑换总上限', 'Total cumulative brush exchange cap')}
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
          {t('画笔已兑换', 'Brushes already exchanged')}: {detail.module_config.brush_exchanged} ·{' '}
          {t('剩余可兑量', 'Remaining')}: {detail.module_config.brush_remaining}
        </p>
        <p>
          {t(
            '上限低于已兑换数量时只停止后续兑换，不追回余额。任务退款不会恢复全站可兑量。',
            'A cap below the exchanged total stops further exchanges without reclaiming balances. Task refunds do not replenish the global cap.',
          )}
        </p>
        {uncertain ? (
          <p role="status">
            {t(
              '保存结果尚未确认。重试将使用同一操作与原始配置。',
              'The save result is unconfirmed. Retry the same operation with its original settings.',
            )}
          </p>
        ) : null}
        {formError || save.error ? <ErrorState error={formError ?? save.error} /> : null}
        {saved ? <p role="status">{t('配置已保存。', 'Settings saved.')}</p> : null}
        <button className="btn btn-primary" type="submit" disabled={save.isPending}>
          {uncertain ? t('重试保存', 'Retry save') : t('保存配置', 'Save settings')}
        </button>
      </form>
    </Card>
  );
}
export function LimitedActivitiesPage({ children }: { readonly children?: ReactNode }) {
  const t = useActivityText(),
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
        title={t('限时活动', 'Limited-time activities')}
        description={t(
          '配置喵帕斯的绘本开放时间与活动币兑换。',
          'Configure the picture book schedule and currency exchanges.',
        )}
        icon="activities"
      />
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
