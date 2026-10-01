import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ReasonText } from './ReasonText';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

it('retains multiline manual history as text alongside an automatic reason', () => {
  const note = 'First line\n<img src=x onerror="run()">\nLast line';
  const { container, rerender } = render(<ReasonText reason={note} />);
  expect(container.querySelector('p')?.textContent).toBe(note);
  expect(container.querySelector('img')).toBeNull();
  rerender(
    <ReasonText
      reason=""
      automatic={{
        kind: 'client_rules',
        schema_version: 1,
        rules: [{ name: 'Confirmed client' }],
        manual_text: note,
      }}
    />,
  );
  expect(screen.getByText('Confirmed client', { exact: true })).toBeInTheDocument();
  expect(container.querySelector('p.ops-blacklist-note')?.textContent).toBe(
    'common.reasons.existingManual: ' + note,
  );
  expect(container.querySelector('img')).toBeNull();
});
