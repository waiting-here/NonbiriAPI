import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { BattleAtmosphere } from './BattleAtmosphere';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (_zh: string, en: string) => en }));

describe('page atmosphere lifecycle', () => {
  it('uses a page-level decoration, updates the state and removes it on exit', () => {
    const { rerender, unmount } = render(<BattleAtmosphere mode="accelerated" reduced={false} />);
    expect(document.body.querySelector(':scope > .likes-atmosphere--accelerated')).not.toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('SPEED MODE');
    rerender(<BattleAtmosphere mode="danger" reduced={false} />);
    expect(document.querySelector('.likes-atmosphere--accelerated')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('OVERLOAD');
    expect(screen.getByRole('status')).not.toHaveTextContent('LOW POWER');
    unmount();
    expect(document.querySelector('.likes-atmosphere')).toBeNull();
  });
  it('keeps static status information when motion is reduced or the page is hidden', () => {
    const { rerender } = render(<BattleAtmosphere mode="danger" reduced />);
    expect(document.querySelector('.likes-atmosphere')).toHaveAttribute('data-paused', 'true');
    expect(screen.getByRole('status')).toHaveTextContent('OVERLOAD');
    rerender(<BattleAtmosphere mode="danger" reduced={false} />);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    fireEvent(document, new Event('visibilitychange'));
    expect(document.querySelector('.likes-atmosphere')).toHaveAttribute('data-paused', 'true');
    rerender(<BattleAtmosphere mode={null} reduced={false} />);
    expect(document.querySelector('.likes-atmosphere')).toBeNull();
  });
});
