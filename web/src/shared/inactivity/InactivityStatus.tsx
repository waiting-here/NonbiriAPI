import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { credits, getStatus, type Policy } from './api';
import './inactivity.css';

export function PolicySummary({ policy, zh }: { policy: Policy; zh: boolean }) {
  return (
    <div className="inactivity-summary">
      {!policy.enabled && <p>{zh ? '低活跃政策未启用。' : 'The inactivity policy is disabled.'}</p>}
      {policy.decay.enabled && (
        <p>
          {zh
            ? `连续 ${policy.decay.inactive_days} 天未活跃后，每 ${policy.decay.interval_days} 天衰减一次。`
            : `After ${policy.decay.inactive_days} inactive days, decay runs every ${policy.decay.interval_days} days.`}
        </p>
      )}
      {policy.decay.enabled &&
        (['general', 'game'] as const).map((asset) => {
          const rule = policy.decay.assets[asset];
          if (!rule) return null;
          const name =
            asset === 'general'
              ? zh
                ? '通用积分'
                : 'General credits'
              : zh
                ? '游戏积分'
                : 'Game credits';
          const amount =
            rule.mode === 'percent' ? `${Number(rule.value) / 100}%` : credits(rule.value);
          return (
            <p key={asset}>
              {name}: {amount} · {zh ? '保留余额' : 'Balance floor'} {credits(rule.floor)}
            </p>
          );
        })}
      {policy.protection.enabled && (
        <p>
          {zh
            ? `连续 ${policy.protection.inactive_days} 天未活跃后，账号将被保护性永久封禁，可联系管理员解封。`
            : `After ${policy.protection.inactive_days} inactive days, the account receives a permanent protective ban. An administrator can restore access.`}
        </p>
      )}
    </div>
  );
}

export function InactivityStatus() {
  const { i18n } = useTranslation();
  const zh = Boolean(i18n.resolvedLanguage?.startsWith('zh'));
  const query = useQuery({
    queryKey: ['inactivity-status'],
    queryFn: ({ signal }) => getStatus(signal),
    staleTime: 60_000,
  });
  const date = (at: number | null) =>
    at === null ? '—' : new Date(at * 1000).toLocaleString(i18n.resolvedLanguage);
  if (query.isPending)
    return <p role="status">{zh ? '正在读取活跃政策…' : 'Loading inactivity policy…'}</p>;
  if (query.isError)
    return (
      <p role="alert">
        {zh ? '暂时无法读取活跃政策。' : 'The inactivity policy could not be loaded.'}
      </p>
    );
  const value = query.data;
  const reasons: Record<string, string> = {
    administrator: zh ? '管理员豁免' : 'Administrator exemption',
    steward: zh ? '协管豁免' : 'Steward exemption',
    banned: zh ? '已封禁，停止衰减' : 'Banned; decay is stopped',
    disabled: zh ? '政策未启用' : 'Policy disabled',
  };
  return (
    <section className="inactivity-panel" aria-label={zh ? '低活跃政策' : 'Inactivity policy'}>
      <h2>{zh ? '账号活跃与保护' : 'Account activity and protection'}</h2>
      <PolicySummary policy={value.configuration} zh={zh} />
      {value.exempt_reason && <p>{reasons[value.exempt_reason] ?? value.exempt_reason}</p>}
      <dl>
        <dt>{zh ? '观察开始' : 'Observation started'}</dt>
        <dd>{date(value.activity.observation_started_at)}</dd>
        <dt>{zh ? '最近主动活跃' : 'Last active action'}</dt>
        <dd>{date(value.activity.last_active_at)}</dd>
        <dt>{zh ? '预计下次衰减' : 'Next projected decay'}</dt>
        <dd>{date(value.decay_at)}</dd>
        <dt>{zh ? '预计保护封禁' : 'Projected protective ban'}</dt>
        <dd>{date(value.protection_at)}</dd>
      </dl>
      <p>
        {zh
          ? '本人成功登录、成功 API 调用、签到或领取福利，以及有效游戏和活动操作可刷新活跃时间。页面轮询和被动捐赠回馈不计入活跃。'
          : 'Successful sign-ins, successful API calls, check-ins, welfare claims, and accepted game or activity actions refresh activity. Page polling and passive donation rewards do not.'}
      </p>
      <p>
        {zh
          ? '衰减仅影响正的可用通用和游戏积分，不扣冻结积分或活动币。新启用或收紧政策至少提供 7 天宽限；恢复活跃会停止后续衰减。捐赠者不因此豁免。'
          : 'Decay affects only positive available general and game credits, excluding frozen funds and activity currencies. Enabling or tightening a policy grants at least seven days of grace. Becoming active stops subsequent decay. Donors are not exempt.'}
      </p>
    </section>
  );
}
