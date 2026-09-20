import { decoded, idempotentOptions, queryPath } from './api';
import { normalizeNumberedPage, validateWindow } from './numberedPage';
import type { PageSize } from './pageNumbers';
import {
  amount,
  array,
  boolean,
  decimal,
  decimalID,
  invalidResponse,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from './wire';

const MAX_MILLI = 9_000_000_000_000_000n;
const termFields = [
  'principal',
  'a',
  'b',
  'nominal',
  'disbursed',
  'fee',
  'repayment',
  'interest',
] as const;
const balanceFields = ['general_before', 'general_after', 'game_before', 'game_after'] as const;

export function loanMilli(value: string): bigint {
  const negative = value.startsWith('-');
  const [whole, fraction = ''] = (negative ? value.slice(1) : value).split('.');
  const magnitude = BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'));
  return negative ? -magnitude : magnitude;
}

function tiers(value: unknown): [string, string, string] {
  const items = array(value, 'loan tiers', 3).map((value) =>
    decimal(value, 'loan tier', { positive: true }),
  );
  if (
    items.length !== 3 ||
    BigInt(items[0]) >= BigInt(items[1]) ||
    BigInt(items[1]) >= BigInt(items[2]) ||
    BigInt(items[2]) * 1000n > MAX_MILLI
  )
    invalidResponse('loan tiers');
  return items as [string, string, string];
}

export function normalizeLoanConfig(root: Record<string, unknown>) {
  const result = {
    loan_enabled: boolean(root.loan_enabled, 'loan switch'),
    loan_tiers: tiers(root.loan_tiers),
    loan_a: amount(root.loan_a, 'disbursement coefficient', false, MAX_MILLI),
    loan_b: amount(root.loan_b, 'repayment coefficient', false, MAX_MILLI),
  };
  const a = loanMilli(result.loan_a),
    b = loanMilli(result.loan_b);
  if (a <= 0n || a >= 1000n || b <= 1000n || BigInt(result.loan_tiers[2]) * b > MAX_MILLI)
    invalidResponse('loan coefficients');
  return result;
}
export type LoanConfig = ReturnType<typeof normalizeLoanConfig>;

export function validLoanConfig(value: LoanConfig): boolean {
  try {
    normalizeLoanConfig(value);
    return true;
  } catch {
    return false;
  }
}

export function normalizeLoanView(value: unknown) {
  const root = record(value, ['enabled', 'available', 'reason', 'tiers'], 'loan availability');
  const enabled = boolean(root.enabled, 'loan enabled');
  const available = boolean(root.available, 'loan available');
  const reason = oneOf(
    root.reason,
    ['available', 'disabled', 'negative_balance', 'ineligible'] as const,
    'loan availability reason',
  );
  if (available !== (reason === 'available') || (!enabled && reason !== 'disabled'))
    invalidResponse('loan availability');
  return { enabled, available, reason, tiers: tiers(root.tiers) };
}
export type LoanView = ReturnType<typeof normalizeLoanView>;

function terms(root: Record<string, unknown>) {
  const values = Object.fromEntries(
    termFields.map((field) => [field, amount(root[field], `loan ${field}`, false, MAX_MILLI)]),
  ) as Record<(typeof termFields)[number], string>;
  const principal = decimal(values.principal, 'loan principal', { positive: true });
  const x = BigInt(principal),
    a = loanMilli(values.a),
    b = loanMilli(values.b);
  const nominal = x * 1000n,
    disbursed = x * a,
    repayment = x * b;
  if (
    a <= 0n ||
    a >= 1000n ||
    b <= 1000n ||
    loanMilli(values.nominal) !== nominal ||
    loanMilli(values.disbursed) !== disbursed ||
    loanMilli(values.repayment) !== repayment ||
    loanMilli(values.fee) !== nominal - disbursed ||
    loanMilli(values.interest) !== repayment - nominal
  )
    invalidResponse('loan terms');
  return values;
}

function financialFacts(root: Record<string, unknown>) {
  const result = {
    ...terms(root),
    ...(Object.fromEntries(
      balanceFields.map((field) => [field, amount(root[field], `loan ${field}`)]),
    ) as Record<(typeof balanceFields)[number], string>),
  };
  if (
    loanMilli(result.general_before) < 0n ||
    loanMilli(result.general_after) !==
      loanMilli(result.general_before) - loanMilli(result.repayment) ||
    loanMilli(result.game_after) !== loanMilli(result.game_before) + loanMilli(result.disbursed)
  )
    invalidResponse('loan balances');
  return result;
}

export function normalizeLoanQuote(value: unknown) {
  const root = record(
    value,
    [...termFields, ...balanceFields, 'quote_token', 'expires_at', 'config_revision', 'as_of'],
    'loan quote',
  );
  const as_of = unixSecond(root.as_of, 'loan quote time'),
    expires_at = unixSecond(root.expires_at, 'loan expiry');
  const quote_token = string(root.quote_token, 'loan quote token', {
    min: 1,
    max: 2048,
    bytes: 2048,
    ascii: true,
  });
  if (expires_at !== as_of + 60 || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(quote_token))
    invalidResponse('loan quote');
  return {
    ...financialFacts(root),
    quote_token,
    as_of,
    expires_at,
    config_revision: decimal(root.config_revision, 'loan revision', { positive: true }),
  };
}
export type LoanQuote = ReturnType<typeof normalizeLoanQuote>;

export function normalizeLoanReceipt(value: unknown) {
  const root = record(
    value,
    [
      ...termFields,
      ...balanceFields,
      'loan_id',
      'operation_id',
      'sequence',
      'created_at',
      'config_revision',
    ],
    'loan receipt',
  );
  return {
    ...financialFacts(root),
    loan_id: opaqueID(root.loan_id, 'loan_', 'loan id'),
    operation_id: opaqueID(root.operation_id, 'op_', 'loan operation'),
    sequence: decimal(root.sequence, 'loan sequence', { positive: true }),
    created_at: unixSecond(root.created_at, 'loan creation'),
    config_revision: decimal(root.config_revision, 'loan revision', { positive: true }),
  };
}
export type LoanReceipt = ReturnType<typeof normalizeLoanReceipt>;

export function quoteLoan(tier: '1' | '2' | '3', signal?: AbortSignal) {
  return decoded('/api/activities/loan/quote', normalizeLoanQuote, {
    method: 'POST',
    json: { tier },
    signal,
  });
}
export function acceptLoan(quote_token: string, key: string) {
  return decoded(
    '/api/activities/loan',
    normalizeLoanReceipt,
    idempotentOptions(key, { method: 'POST', json: { quote_token } }),
  );
}
export function getLoans(
  role: 'owner' | 'admin' | 'steward',
  userID: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
) {
  validateWindow(page, pageSize);
  const path =
    role === 'owner'
      ? '/api/activities/loans'
      : `${role === 'admin' ? '/admin/api' : '/api/steward'}/users/${encodeURIComponent(decimalID(userID, 'loan owner'))}/loans`;
  return decoded(
    queryPath(path, { page, page_size: pageSize }),
    (value) =>
      normalizeNumberedPage(
        value,
        'loans',
        normalizeLoanReceipt,
        page,
        pageSize,
        (loan) => loan.loan_id,
      ),
    { signal },
  );
}
