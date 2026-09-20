import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { getLoans, type LoanQuote, type LoanReceipt } from '@shared/operations/loans';
import { usePagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { ErrorState, LoadingState } from './States';
import { useLoanText } from './loanCopy';
import './loan.css';

export function LoanFacts({ loan }: { loan: LoanQuote | LoanReceipt }) {
  const text = useLoanText();
  const fields = [
    [text('名义借款', 'Nominal loan'), loan.nominal],
    [text('游戏积分到账', 'Game credits received'), loan.disbursed],
    [text('手续费', 'Fee'), loan.fee],
    [
      text('扣除通用积分（本息合计）', 'General credits deducted (principal and interest)'),
      loan.repayment,
    ],
    [text('利息', 'Interest'), loan.interest],
    [text('到账系数 A', 'Disbursement coefficient A'), loan.a],
    [text('本息系数 B', 'Repayment coefficient B'), loan.b],
    [
      text('通用积分：成交前 → 成交后', 'General credits: before → after'),
      `${loan.general_before} → ${loan.general_after}`,
    ],
    [
      text('游戏积分：成交前 → 成交后', 'Game credits: before → after'),
      `${loan.game_before} → ${loan.game_after}`,
    ],
  ];
  return (
    <dl className="loan-facts">
      {fields.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function LoanDialog({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null),
    id = useId();
  const text = useLoanText();
  useEffect(() => {
    const previous = document.activeElement,
      dialog = ref.current;
    dialog?.showModal();
    return () => {
      dialog?.close();
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="loan-dialog"
      aria-labelledby={id}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <header>
        <h2 id={id}>{title}</h2>
        <button className="btn btn-secondary" onClick={onClose} autoFocus>
          {text('关闭', 'Close')}
        </button>
      </header>
      {children}
    </dialog>
  );
}

function LoanHistoryContent({
  role,
  account,
  userID,
}: {
  role: 'owner' | 'admin' | 'steward';
  account: string;
  userID: string;
}) {
  const text = useLoanText(),
    client = useQueryClient();
  const frame = role === 'admin' ? 'admin' : 'steward';
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'loans',
    scopeKey: `${account}:${userID}`,
  });
  const query = useQuery({
    queryKey: [
      role === 'admin' ? 'admin' : 'user',
      'loans',
      role,
      account,
      userID,
      pager.page,
      pager.pageSize,
    ],
    queryFn: async ({ signal }) => {
      const session = captureStationSession(client, frame);
      try {
        const result = await getLoans(role, userID, pager.page, pager.pageSize, signal);
        if (!stationSessionMatches(client, frame, session)) throw new StationSessionChangedError();
        return result;
      } catch (error) {
        if (!stationSessionMatches(client, frame, session)) throw new StationSessionChangedError();
        if (isUnauthorized(error) || isForbidden(error)) clearStationSession(client, frame);
        throw error;
      }
    },
    retry: false,
  });
  return (
    <>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          {query.data.data.length === 0 ? (
            <p>{text('暂无借款记录。', 'No loans yet.')}</p>
          ) : (
            query.data.data.map((loan) => (
              <article className="loan-record" key={loan.loan_id}>
                <h3>{formatDateTime(loan.created_at)}</h3>
                <LoanFacts loan={loan} />
                <dl className="loan-facts">
                  <div>
                    <dt>{text('借款编号', 'Loan ID')}</dt>
                    <dd>{loan.loan_id}</dd>
                  </div>
                  <div>
                    <dt>{text('账务流水编号', 'Ledger operation')}</dt>
                    <dd>{loan.operation_id}</dd>
                  </div>
                  <div>
                    <dt>{text('流水序号', 'Sequence')}</dt>
                    <dd>{loan.sequence}</dd>
                  </div>
                  <div>
                    <dt>{text('配置版本', 'Configuration revision')}</dt>
                    <dd>{loan.config_revision}</dd>
                  </div>
                </dl>
              </article>
            ))
          )}
          <PagePagination
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
        </>
      )}
    </>
  );
}

export function LoanHistory({
  role,
  account,
  userID,
}: {
  role: 'owner' | 'admin' | 'steward';
  account: string;
  userID: string;
}) {
  const [open, setOpen] = useState(false),
    text = useLoanText();
  return (
    <>
      <button className="btn btn-secondary" onClick={() => setOpen(true)}>
        {text('借款明细', 'Loan history')}
      </button>
      {open ? (
        <LoanDialog title={text('借款明细', 'Loan history')} onClose={() => setOpen(false)}>
          <LoanHistoryContent role={role} account={account} userID={userID} />
        </LoanDialog>
      ) : null}
    </>
  );
}
