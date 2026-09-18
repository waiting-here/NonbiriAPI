import { ApiError } from '@shared/query/http';
import type { Profile } from './types';
import { useDuelText } from './copy';
import { entryMessage, type EntryProblem } from './availability';

export function DuelFeedback({
  error,
  uncertain = false,
  pending = false,
  onRetry,
  queueAttempt = false,
  entryProblem,
}: {
  readonly error: unknown;
  readonly uncertain?: boolean;
  readonly pending?: boolean;
  readonly onRetry: () => void;
  readonly queueAttempt?: boolean;
  readonly entryProblem?: EntryProblem | null;
}) {
  const t = useDuelText();
  if (!error && !pending) return null;
  const code = error instanceof ApiError ? error.code : '';
  const message = uncertain
    ? t(
        '响应尚未确认。请用原请求重试，确认后继续。',
        'The response is unconfirmed. Retry the same request before continuing.',
      )
    : code === 'conflict'
      ? queueAttempt
        ? entryProblem
          ? entryMessage(entryProblem, t)
          : t(
              '匹配条件已更新，请核对后重试。',
              'Matching conditions changed. Review them and try again.',
            )
        : t(
            '对局状态已变化，请按刷新后的状态继续。',
            'The game state changed. Continue with the refreshed state.',
          )
      : code === 'insufficient_credits'
        ? t('可用积分不足，请检查钱包。', 'Insufficient available credits. Check your wallets.')
        : code === 'maintenance' || code === 'service_unavailable'
          ? t(
              '暂时无法开始新局；现有对局按服务端状态继续。',
              'New games are unavailable. Existing games follow their current state.',
            )
          : code === 'rate_limited' || code === 'resource_limit'
            ? t('当前请求较多，请稍后再试。', 'Too many requests right now. Try again shortly.')
            : code === 'unauthorized' || code === 'forbidden'
              ? t(
                  '当前账号无法执行此操作，请刷新登录状态。',
                  'This account cannot perform this action. Refresh your sign-in status.',
                )
              : t(
                  '暂未取得有效响应，请重试同步。',
                  'A valid response could not be obtained. Retry syncing.',
                );
  return (
    <div className="duel-feedback" role="status">
      <span>{pending ? t('正在确认…', 'Confirming…') : message}</span>
      {!pending && (
        <button type="button" className="btn btn-secondary" onClick={onRetry}>
          {uncertain ? t('重试原请求', 'Retry same request') : t('刷新状态', 'Refresh state')}
        </button>
      )}
    </div>
  );
}
export function DuelProfile({
  profile,
  you = false,
}: {
  readonly profile: Profile;
  readonly you?: boolean;
}) {
  const t = useDuelText();
  return (
    <span className="duel-profile">
      {profile.avatarURL && (
        <img src={profile.avatarURL} alt="" referrerPolicy="no-referrer" width="24" height="24" />
      )}
      <span>
        {you
          ? t('你', 'You')
          : profile.kind === 'public'
            ? profile.displayName
            : profile.kind === 'deleted'
              ? t('已删除账号', 'Deleted account')
              : t('匿名玩家', 'Anonymous player')}
      </span>
    </span>
  );
}
