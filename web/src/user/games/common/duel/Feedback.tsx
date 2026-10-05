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
  const text = useDuelText();
  if (!error && !pending) return null;
  const code = error instanceof ApiError ? error.code : '';
  const message = uncertain
    ? text('common.theResponseIsUnconfirmedRetryTheSame')
    : code === 'conflict'
      ? queueAttempt
        ? entryProblem
          ? entryMessage(entryProblem, text)
          : text('common.matchingConditionsChangedReviewThemAndTry')
        : text('common.theGameStateChangedContinueWithThe')
      : code === 'insufficient_credits'
        ? text('common.insufficientAvailableCreditsCheckYourWallets')
        : code === 'maintenance' || code === 'service_unavailable'
          ? text('common.newGamesAreUnavailableExistingGamesFollow')
          : code === 'rate_limited' || code === 'resource_limit'
            ? text('common.tooManyRequestsRightNowTryAgain')
            : code === 'unauthorized' || code === 'forbidden'
              ? text('common.thisAccountCannotPerformThisActionRefresh')
              : text('common.aValidResponseCouldNotBeObtained');
  return (
    <div className="duel-feedback" role="status">
      <span>{pending ? text('common.confirming') : message}</span>
      {!pending && (
        <button type="button" className="btn btn-secondary" onClick={onRetry}>
          {uncertain ? text('common.retrySameRequest') : text('common.refreshState')}
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
  const text = useDuelText();
  return (
    <span className="duel-profile">
      {profile.avatarURL && (
        <img src={profile.avatarURL} alt="" referrerPolicy="no-referrer" width="24" height="24" />
      )}
      <span>
        {you
          ? text('bidding.you')
          : profile.kind === 'public' || profile.kind === 'ai'
            ? profile.displayName
            : profile.kind === 'deleted'
              ? text('common.deletedAccount')
              : text('common.anonymousPlayer')}
      </span>
    </span>
  );
}
