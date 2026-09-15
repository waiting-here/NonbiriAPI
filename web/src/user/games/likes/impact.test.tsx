import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CastImpact } from './CastImpact';
import { presentationValue } from './normalize';
import wire from './testdata/authority.json';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (_zh: string, en: string) => en }));

describe('cast impact presentation', () => {
  it('never celebrates an overloaded or cancelled cast', () => {
    const events = presentationValue(wire.scenarios.single_overload.summary).events.filter(
      (event) => event.seat === 1,
    );
    const { container } = render(
      <CastImpact
        events={events}
        from={20}
        to={20}
        target={60}
        progress={0.3}
        reduced={false}
        overloaded
      />,
    );
    expect(container.querySelector('.likes-hit')).toBeNull();
    expect(screen.queryByText('SKILL CAST')).not.toBeInTheDocument();
    expect(screen.getByText('OVERLOAD')).toBeInTheDocument();
  });
  it('animates the recorded gain without deriving it from skill costs or buff arithmetic', () => {
    const events = presentationValue(wire.scenarios.chain.summary).events.filter(
      (event) => event.seat === 0 && event.cast,
    );
    const { rerender } = render(
      <CastImpact events={events} from={8} to={45} target={60} progress={0.2} reduced={false} />,
    );
    expect(screen.getByText('LIKES SURGE')).toBeInTheDocument();
    const first = screen.getByLabelText('Likes change: 37').textContent;
    rerender(
      <CastImpact events={events} from={8} to={45} target={60} progress={0.7} reduced={false} />,
    );
    expect(screen.getByLabelText('Likes change: 37').textContent).not.toBe(first);
    rerender(<CastImpact events={events} from={8} to={45} target={60} progress={0.2} reduced />);
    expect(screen.getByLabelText('Likes change: 37')).toHaveTextContent('+37');
    rerender(<CastImpact events={events} from={45} to={71} target={60} progress={1} reduced />);
    expect(screen.getByText('TARGET REACHED')).toBeInTheDocument();
    expect(screen.getByLabelText('Likes change: 26')).toHaveTextContent('+26');
  });
});
