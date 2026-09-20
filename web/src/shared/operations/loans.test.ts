import { describe, expect, it } from 'vitest';
import { loanQuote, loanReceipt } from '../../../test/fixtures/loans';
import {
  normalizeLoanConfig,
  normalizeLoanQuote,
  normalizeLoanReceipt,
  normalizeLoanView,
} from './loans';

describe('loan financial wire', () => {
  it('keeps wide balances and ledger sequences exact and reconciles both assets', () => {
    const receipt = loanReceipt();
    receipt.general_before = '9007199254740993.999';
    receipt.general_after = '9007199254727993.999';
    expect(normalizeLoanReceipt(receipt)).toEqual(receipt);
    expect(() => normalizeLoanReceipt({ ...receipt, general_after: '9007199254727994' })).toThrow();
    expect(() => normalizeLoanReceipt({ ...receipt, game_after: '8999.999' })).toThrow();
    expect(() => normalizeLoanReceipt({ ...receipt, sequence: 9007199254740992 })).toThrow();
  });
  it('validates every term and expiry without rounding or accepting extra fields', () => {
    expect(normalizeLoanQuote(loanQuote())).toEqual(loanQuote());
    for (const field of [
      'principal',
      'nominal',
      'disbursed',
      'fee',
      'repayment',
      'interest',
      'a',
      'b',
    ]) {
      expect(() => normalizeLoanQuote({ ...loanQuote(), [field]: '0.001' })).toThrow();
    }
    expect(() =>
      normalizeLoanQuote({ ...loanQuote(), expires_at: loanQuote().expires_at + 1 }),
    ).toThrow();
    expect(() => normalizeLoanQuote({ ...loanQuote(), private_nonce: 'hidden' })).toThrow();
    expect(() =>
      normalizeLoanView({
        enabled: false,
        available: true,
        reason: 'available',
        tiers: ['1', '2', '3'],
      }),
    ).toThrow();
  });
  it('rejects invalid or overflowing configuration even while loans are disabled', () => {
    const config = {
      loan_enabled: false,
      loan_tiers: ['1', '2', '1000'],
      loan_a: '0.001',
      loan_b: '9000000000',
    };
    expect(normalizeLoanConfig(config)).toEqual(config);
    for (const patch of [
      { loan_a: '0' },
      { loan_a: '1' },
      { loan_a: '0.0001' },
      { loan_b: '1' },
      { loan_b: '9000000000.001' },
      { loan_tiers: ['1', '1', '2'] },
      { loan_tiers: ['1', '2'] },
    ]) {
      expect(() => normalizeLoanConfig({ ...config, ...patch })).toThrow();
    }
  });
});
