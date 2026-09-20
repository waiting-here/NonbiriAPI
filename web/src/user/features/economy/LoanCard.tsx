import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { LoanFacts, LoanHistory } from '@shared/components/LoanDetails';
import { loanReasons, useLoanText } from '@shared/components/loanCopy';
import { Card, ErrorState } from '@shared/components/States';
import {
  acceptLoan,
  quoteLoan,
  type LoanQuote,
  type LoanReceipt,
  type LoanView,
} from '@shared/operations/loans';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ApiError } from '@shared/query/http';
import { economyKeys, economySessionRequest } from './queries';

export function LoanCard({
  loan,
  account,
  masterAvailable,
}: {
  loan: LoanView;
  account: string;
  masterAvailable: boolean;
}) {
  const text = useLoanText(),
    client = useQueryClient();
  const [tier, setTier] = useState<'1' | '2' | '3'>('1');
  const [quote, setQuote] = useState<{ value: LoanQuote; received: number } | null>(null);
  const [receipt, setReceipt] = useState<LoanReceipt | null>(null);
  const [reason, setReason] = useState<number | null>(null);
  const [now, setNow] = useState(Date.now);
  const [conflict, setConflict] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  useEffect(() => {
    if (!quote) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [quote]);
  const request = useMutation({
    mutationFn: () => economySessionRequest(client, () => quoteLoan(tier), account),
    retry: false,
    onSuccess: (value) => {
      const received = Date.now();
      setQuote({ value, received });
      setNow(received);
      setReason(null);
      setUncertain(false);
      setDialogOpen(true);
    },
  });
  const borrow = useRetainedOperation(
    (token: string, key) => economySessionRequest(client, () => acceptLoan(token, key), account),
    async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: economyKeys.activities }),
        client.invalidateQueries({ queryKey: ['user', 'games'] }),
        client.invalidateQueries({ queryKey: ['user', 'credits'] }),
        client.invalidateQueries({ queryKey: ['user', 'loans'] }),
      ]);
    },
    ['user', 'economy'],
  );
  const busy = request.isPending || borrow.isPending;
  const expired =
    quote !== null && now - quote.received >= (quote.value.expires_at - quote.value.as_of) * 1000;
  const renew = () => {
    setConflict(true);
    setReason(null);
    borrow.reset();
    request.mutate();
  };
  const confirm = () => {
    if (!quote || busy) return;
    // A retry of an uncertain response must preserve the exact signed quote
    // and mutation identity, including after its ordinary acceptance expiry.
    if (expired && !uncertain) {
      renew();
      return;
    }
    borrow.mutate(quote.value.quote_token, {
      onSuccess: (value) => {
        setReceipt(value);
        setQuote(null);
        setReason(null);
        setUncertain(false);
        setDialogOpen(false);
      },
      onError: (error) => {
        const unknown = responseOutcomeUnknown(error);
        setUncertain(unknown);
        if (!unknown && error instanceof ApiError && error.status === 409) renew();
      },
    });
  };
  const available = loan.available && masterAvailable;
  return (
    <Card className="loan-card">
      <h2>{text('赛博网贷', 'Cyber loan')}</h2>
      <p>{text('升！升舱的钱我来出！', 'Upgrade! I’ll cover your ticket!')}</p>
      {!available ? (
        <p>
          {loan.reason === 'negative_balance'
            ? text(
                '通用积分为负，暂时无法再次借款。',
                'Your general balance is negative. Another loan is unavailable.',
              )
            : text('当前无法借款。', 'Borrowing is currently unavailable.')}
        </p>
      ) : null}
      <label>
        {text('借款额度', 'Loan amount')}{' '}
        <select
          value={tier}
          disabled={!available || busy || quote !== null}
          onChange={(event) => setTier(event.target.value as '1' | '2' | '3')}
        >
          {loan.tiers.map((value, index) => (
            <option value={String(index + 1)} key={value}>
              {value}
            </option>
          ))}
        </select>
      </label>
      <div className="loan-actions">
        <button
          className="btn btn-primary"
          disabled={!available || busy || quote !== null}
          onClick={() => {
            setReceipt(null);
            setConflict(false);
            borrow.reset();
            request.mutate();
          }}
        >
          {text('我要借款', 'Get a loan')}
        </button>
        {uncertain && !dialogOpen ? (
          <button className="btn btn-primary" onClick={() => setDialogOpen(true)}>
            {text('核对这笔借款', 'Check this loan')}
          </button>
        ) : null}
        <LoanHistory role="owner" account={account} userID={account} />
      </div>
      {request.error ? <ErrorState error={request.error} /> : null}
      {receipt ? (
        <div role="status">
          <h3>{text('借款已到账', 'Loan received')}</h3>
          <LoanFacts loan={receipt} />
        </div>
      ) : null}
      <ConfirmDialog
        open={dialogOpen && quote !== null}
        title={text('确认借款', 'Confirm loan')}
        description={text('请选择是否借入以下额度。', 'Confirm the following loan amount.')}
        confirmLabel={
          uncertain
            ? text('核对这笔借款', 'Check this loan')
            : expired
              ? text('刷新借款方案', 'Refresh quote')
              : text('确认借款', 'Confirm loan')
        }
        busy={busy}
        onConfirm={confirm}
        onCancel={() => {
          setDialogOpen(false);
          setReason(null);
          if (!uncertain) setQuote(null);
        }}
        cancelLabel={uncertain ? text('稍后核对', 'Check later') : undefined}
      >
        {quote ? (
          <>
            <p className="loan-nominal">
              <strong>{quote.value.nominal}</strong>
              <button
                className="btn btn-quiet loan-asterisk"
                aria-label={text('查看费用与余额详情', 'View fees and balance details')}
                aria-expanded={reason !== null}
                aria-controls="loan-quote-details"
                onClick={() =>
                  setReason(reason === null ? Math.floor(Math.random() * loanReasons.length) : null)
                }
              >
                *
              </button>
            </p>
            {reason !== null ? (
              <section id="loan-quote-details" className="loan-details">
                <p>{text(loanReasons[reason][0], loanReasons[reason][1])}</p>
                <LoanFacts loan={quote.value} />
                <p>
                  {text(
                    '余额为当前预估，实际成交明细以成交时的余额为准。',
                    'Balances are estimates. The receipt uses your balances at the time of the transaction.',
                  )}
                </p>
              </section>
            ) : null}
            {conflict ? (
              <p role="status">
                {text(
                  '借款方案已刷新，请重新确认。',
                  'The quote was refreshed. Please confirm again.',
                )}
              </p>
            ) : null}
            {expired && !uncertain ? (
              <p role="status">
                {text(
                  '方案已过期，请刷新后重新确认。',
                  'This quote expired. Refresh it and confirm again.',
                )}
              </p>
            ) : null}
            {uncertain ? (
              <p role="alert">
                {text(
                  '尚未确认这笔借款的结果。请核对原请求，避免重复借款。',
                  'This loan’s outcome is not yet confirmed. Check the original request before borrowing again.',
                )}
              </p>
            ) : null}
            {borrow.error ? <ErrorState error={borrow.error} /> : null}
          </>
        ) : null}
      </ConfirmDialog>
    </Card>
  );
}
