import { describe, expect, it } from 'vitest';
import { entryProblem } from './availability';

describe('duel admission', () => {
  const mode = {
    enabled: true,
    available: true,
    ticket: '0',
    rates: { platform: 0, welfare: 0, thursday: 0 },
    termsHash: '',
    contentHash: '',
  };
  const context = {
    accepting: true,
    config: {
      enabled: true,
      available: true,
      modes: { quick: mode, standard: { ...mode, enabled: false } },
    },
  };
  it('requires every admission flag while keeping the independent mode open', () => {
    expect(entryProblem(context, 'quick')).toBeNull();
    expect(entryProblem(context, 'standard')).toBe('mode-closed');
    expect(entryProblem(context, 'missing')).toBe('mode-closed');
    expect(entryProblem({ ...context, accepting: false }, 'quick')).toBe('game-closed');
    expect(
      entryProblem({ ...context, config: { ...context.config, enabled: false } }, 'quick'),
    ).toBe('game-closed');
    expect(
      entryProblem({ ...context, config: { ...context.config, available: false } }, 'quick'),
    ).toBe('unavailable');
    expect(
      entryProblem(
        {
          ...context,
          config: { ...context.config, modes: { quick: { ...mode, available: false } } },
        },
        'quick',
      ),
    ).toBe('unavailable');
  });
});
