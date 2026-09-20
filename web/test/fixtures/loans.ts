export function loanFacts() {
  return {
    principal: '10000',
    nominal: '10000',
    a: '0.9',
    b: '1.3',
    disbursed: '9000',
    fee: '1000',
    repayment: '13000',
    interest: '3000',
    general_before: '0',
    general_after: '-13000',
    game_before: '0',
    game_after: '9000',
    config_revision: '2',
  };
}
export function loanQuote() {
  return {
    ...loanFacts(),
    quote_token: 'cXVvdGU.c2lnbmF0dXJl',
    as_of: 1_800_000_000,
    expires_at: 1_800_000_060,
  };
}
export function loanReceipt() {
  return {
    ...loanFacts(),
    loan_id: `loan_${'A'.repeat(22)}`,
    operation_id: `op_${'A'.repeat(22)}`,
    sequence: '9007199254740993',
    created_at: 1_800_000_005,
  };
}
export function loanActivity() {
  return {
    master: { enabled: true, available: true, reason: 'available' },
    loan: {
      enabled: true,
      available: true,
      reason: 'available',
      tiers: ['10000', '100000', '1000000'],
    },
    welfare: {
      asset_type: 'game',
      pool_asset_type: 'general',
      enabled: false,
      state: 'unavailable',
      site_day: '',
      threshold: '0',
      cap: '0',
      pool_balance: '0',
      claimed_today: false,
    },
    thursday: {
      enabled: false,
      state: 'unavailable',
      server_now: 1_800_000_000,
      current: null,
      next: null,
      last_result: null,
    },
  };
}
