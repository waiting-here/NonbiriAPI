import { useRef, useState } from 'react';
import { isApiError } from '@shared/query/http';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { DataTable } from '@shared/components/ui/DataTable';
import { Segmented } from '@shared/components/ui/Segmented';
import { formatCompact, formatCount } from '@shared/utils/formatNumber';
import { useGameCopy } from '../copy';
import { PublicGameIdentity } from '../common/PublicGameIdentity';
import { GamePrivacyControl } from '../common/GamePrivacyControl';
import { isResponseUnknown, isServiceStopped } from '../common/request';
import { returnFishingBatch, returnFishingOutcome, useFishingPondLeaderboard } from './api';
import type {
  FishingOutcome,
  FishingPondBoard,
  FishingPondPeriod,
  FishingPondReceipt,
} from './types';

export function FishingGiftSource({ outcome }: { readonly outcome: FishingOutcome }) {
  const { language, text } = useGameCopy();
  if (outcome.pond?.returnState !== 'gift') return null;
  return (
    <div className="fishing-gift-source">
      <span>{text('fishing.pond.giftFrom')}</span>
      <PublicGameIdentity
        identity={outcome.pond.gift ?? { kind: 'anonymous' }}
        anonymousLabel={text('fishing.pond.anonymousSource')}
        size={24}
      />
      {language === 'zh' ? <span>{text('fishing.pond.giftSuffix')}</span> : null}
    </div>
  );
}

export function FishingPondDisclosure({
  outcomes,
  isPublic,
}: {
  readonly outcomes: readonly FishingOutcome[];
  readonly isPublic: boolean;
}) {
  const { text } = useGameCopy();
  const available = outcomes.filter((outcome) => outcome.pond?.returnState === 'available');
  const hasTrash = available.some((outcome) => outcome.speciesKey !== 'fry');
  if (!available.length || (isPublic && !hasTrash)) return null;
  return (
    <div className="fishing-pond-disclosure nb-small nb-muted">
      {isPublic ? (
        <p>{text('fishing.pond.disclosure')}</p>
      ) : (
        <>
          <p>{text('fishing.pond.anonymousHelp')}</p>
          <GamePrivacyControl />
        </>
      )}
    </div>
  );
}

export function FishingPondReturn({
  batchID,
  outcome,
  isPublic,
  onReceipt,
  busy = false,
  onSending,
}: {
  readonly batchID: string;
  readonly outcome: FishingOutcome;
  readonly isPublic: boolean;
  readonly onReceipt: (receipt: FishingPondReceipt) => void;
  readonly busy?: boolean;
  readonly onSending?: (sending: boolean) => void;
}) {
  const { text } = useGameCopy();
  const [receipt, setReceipt] = useState<FishingPondReceipt | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const inFlight = useRef(false);
  const state = receipt?.returnState ?? outcome.pond?.returnState;
  if (!state || state === 'gift') return null;
  if (state === 'returned' || state === 'released')
    return (
      <div className="fishing-pond-return" role="status">
        {text(state === 'released' ? 'fishing.pond.released' : 'fishing.pond.returned')}
      </div>
    );
  const fry = outcome.speciesKey === 'fry';
  const unknown = error !== null && isResponseUnknown(error);
  const send = async () => {
    if (inFlight.current || busy) return;
    inFlight.current = true;
    setSending(true);
    onSending?.(true);
    setError(null);
    try {
      const response = await returnFishingOutcome(batchID, outcome.ordinal);
      setReceipt(response);
      onReceipt(response);
    } catch (error) {
      setError(error);
    } finally {
      inFlight.current = false;
      setSending(false);
      onSending?.(false);
    }
  };
  const errorKey = unknown
    ? 'fishing.pond.unknown'
    : isApiError(error) && error.code === 'forbidden'
      ? 'fishing.pond.forbidden'
      : isApiError(error) && error.code === 'not_found'
        ? 'fishing.pond.unavailable'
        : 'fishing.pond.failed';
  return (
    <div className="fishing-pond-return">
      <button
        type="button"
        className="nb-btn nb-btn--secondary nb-btn--sm"
        disabled={sending || busy || (!isPublic && !unknown)}
        onClick={() => void send()}
      >
        {text(
          sending
            ? 'fishing.pond.sending'
            : error
              ? 'fishing.pond.retry'
              : fry
                ? 'fishing.pond.release'
                : 'fishing.pond.return',
        )}
      </button>
      {fry ? <small>{text('fishing.pond.releaseHelp')}</small> : null}
      {error ? (
        <p className="fishing-pond-error" role="alert">
          {text(errorKey)}
        </p>
      ) : null}
    </div>
  );
}

export function FishingPondBatchReturn({
  batchID,
  outcomes,
  isPublic,
  busy,
  onSending,
  onReceipts,
}: {
  readonly batchID: string;
  readonly outcomes: readonly FishingOutcome[];
  readonly isPublic: boolean;
  readonly busy: boolean;
  readonly onSending: (sending: boolean) => void;
  readonly onReceipts: (receipts: readonly FishingPondReceipt[]) => void;
}) {
  const { text } = useGameCopy();
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const inFlight = useRef(false);
  const available = outcomes.filter((outcome) => outcome.pond?.returnState === 'available');
  if (available.length < 2 && !error) return null;
  const fry = available.filter((outcome) => outcome.speciesKey === 'fry').length;
  const unknown = error !== null && isResponseUnknown(error);
  const send = async () => {
    if (inFlight.current || busy) return;
    inFlight.current = true;
    setSending(true);
    onSending(true);
    setError(null);
    try {
      const response = await returnFishingBatch(batchID);
      onReceipts(response.receipts);
    } catch (error) {
      setError(error);
    } finally {
      inFlight.current = false;
      setSending(false);
      onSending(false);
    }
  };
  const errorKey = unknown
    ? 'fishing.pond.batchUnknown'
    : isApiError(error) && error.code === 'forbidden'
      ? 'fishing.pond.forbidden'
      : isApiError(error) && error.code === 'not_found'
        ? 'fishing.pond.batchUnavailable'
        : 'fishing.pond.batchFailed';
  return (
    <div className="fishing-pond-return fishing-pond-batch-return">
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={sending || busy || (!isPublic && !unknown)}
        onClick={() => void send()}
      >
        {text(
          sending
            ? 'fishing.pond.sending'
            : error
              ? 'fishing.pond.batchRetry'
              : fry === 0
                ? 'fishing.pond.batchReturn'
                : fry === available.length
                  ? 'fishing.pond.batchRelease'
                  : 'fishing.pond.batchMixed',
          { count: formatCount(available.length).display },
        )}
      </button>
      {error ? (
        <p className="fishing-pond-error" role="alert">
          {text(errorKey)}
        </p>
      ) : null}
    </div>
  );
}

export function FishingPondBoard({ board }: { readonly board: FishingPondBoard }) {
  const { text } = useGameCopy();
  const [period, setPeriod] = useState<FishingPondPeriod>('7d');
  const query = useFishingPondLeaderboard(board, period);
  const title = text(board === 'cleanup' ? 'fishing.pond.cleanup' : 'fishing.pond.gifts');
  const entries = query.data?.entries ?? [];
  const me = query.data?.me;
  const rows = me && !entries.some((row) => row.isMe) ? [...entries, me] : entries;
  const myRow = rows.find((row) => row.isMe);
  return (
    <Card className="fishing-board fishing-pond-board">
      <div className="fishing-board__heading">
        <h2>{title}</h2>
        <button
          type="button"
          className="nb-btn nb-btn--secondary fishing-board__refresh"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {text(
            query.isFetching ? 'fishing.leaderboard.refreshing' : 'fishing.leaderboard.refresh',
          )}
        </button>
      </div>
      <Segmented
        label={text('fishing.pond.period')}
        value={period}
        onChange={setPeriod}
        options={[
          { value: '7d', label: text('fishing.pond.days7') },
          { value: '30d', label: text('fishing.pond.days30') },
          { value: 'all', label: text('fishing.pond.all') },
        ]}
      />
      <p className="nb-small nb-muted">
        {text(board === 'cleanup' ? 'fishing.pond.cleanupHelp' : 'fishing.pond.giftsHelp')}
      </p>
      {query.isPending ? (
        <LoadingState label={text('common.loading')} />
      ) : query.error ? (
        isServiceStopped(query.error) ? (
          <p className="game-inline-notice game-inline-notice--warning">
            {text('common.serviceStopped')}
          </p>
        ) : (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        )
      ) : rows.length === 0 ? (
        <EmptyState
          title={text('fishing.pond.empty')}
          body={text(board === 'cleanup' ? 'fishing.pond.emptyCleanup' : 'fishing.pond.emptyGifts')}
        />
      ) : (
        <DataTable
          caption={title}
          rows={rows}
          selectedKey={myRow ? `${myRow.rank}-me` : undefined}
          rowKey={(row) => `${row.rank}-${row.isMe ? 'me' : 'row'}`}
          columns={[
            {
              key: 'rank',
              header: '#',
              cell: 'meta',
              align: 'nowrap',
              render: (row) => formatCount(row.rank).display,
            },
            {
              key: 'identity',
              header: text('fishing.leaderboard.angler'),
              cell: 'title',
              render: (row) => (
                <PublicGameIdentity
                  identity={row.identity}
                  anonymousLabel={text('fishing.leaderboard.anonymous')}
                  isMe={row.isMe}
                  meLabel={text('fishing.leaderboard.me')}
                />
              ),
            },
            {
              key: 'count',
              header: text('fishing.pond.itemCount'),
              cell: 'meta',
              align: 'num',
              mobileLabel: text('fishing.pond.itemCount'),
              render: (row) => {
                const count = formatCompact(row.itemCount);
                return (
                  <span className="fishing-pond-count" title={count.exact}>
                    {count.display}
                  </span>
                );
              },
            },
          ]}
        />
      )}
    </Card>
  );
}
