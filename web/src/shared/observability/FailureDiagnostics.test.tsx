import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FailureDetails, RequestDelivery } from './FailureDiagnostics';
import { normalizeDelivery, normalizeFailure } from './failure';

vi.mock('react-i18next', async () => {
  const { default: copy } = await import('@shared/i18n/common/en.json');
  return {
    useTranslation: () => ({
      t: (key: string, values?: Record<string, unknown>) => {
        const value = key
          .split('.')
          .reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], copy);
        return typeof value === 'string'
          ? value.replace(/\{\{(.*?)\}\}/g, (_, name: string) => String(values?.[name] ?? ''))
          : key;
      },
    }),
  };
});

const interrupted = {
  category: 'network',
  phase: 'read_response',
  code: 'connection_reset',
  message: 'Connection was reset by the peer',
  causes: [
    { type: '*net.OpError', code: 'connection_reset', message: 'Connection was reset by the peer' },
  ],
  secondary: [
    { type: '*errors.errorString', code: 'canceled', message: 'Request context was canceled' },
  ],
  upstream_status: 200,
  headers_received: true,
  response_started: true,
  body_eof: false,
  protocol_complete: false,
  usage_present: true,
  received_bytes: 12345,
  received_events: 3,
  forwarded_bytes: 12,
  started_at_ms: 1800000000000,
  failed_at_ms: 1800000001000,
  correlation: [{ source: 'upstream', name: 'x-request-id', value: 'sample-upstream-id' }],
};

describe('failure observations', () => {
  it('explains a read failure after HTTP 200 without treating it as completion', () => {
    render(<FailureDetails failure={normalizeFailure(interrupted)} />);
    expect(screen.getByText('The peer reset the connection')).toBeVisible();
    expect(screen.getByText(/This site started its response/)).toBeVisible();
    expect(screen.getByText('Completion was not confirmed')).toBeVisible();
    const evidence = screen.getByText('Technical evidence').closest('details')!;
    expect(evidence).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Technical evidence'));
    expect(screen.getByText('12,345')).toBeVisible();
    expect(screen.getByText('sample-upstream-id')).toBeVisible();
    expect(within(evidence).getByText('Request context was canceled')).toBeVisible();
    expect(screen.getByText(/2027-01-15T08:00:01.000Z/)).toBeVisible();
  });

  it('keeps clean EOF distinct from the missing protocol end signal', () => {
    render(
      <FailureDetails
        failure={normalizeFailure({
          ...interrupted,
          category: 'protocol',
          code: 'invalid_response',
          body_eof: true,
          response_started: false,
        })}
      />,
    );
    expect(screen.getByText('Completion was not confirmed')).toBeVisible();
    fireEvent.click(screen.getByText('Technical evidence'));
    expect(screen.getByText('End of byte stream observed').nextElementSibling).toHaveTextContent(
      'Yes',
    );
  });

  it('gives a compression-specific conclusion and correction', () => {
    render(
      <FailureDetails
        failure={normalizeFailure({
          ...interrupted,
          category: 'encoding',
          code: 'compression_checksum_invalid',
        })}
      />,
    );
    expect(screen.getByText('Response compression integrity check failed')).toBeVisible();
    expect(
      screen.getByText('Check the service response encoding and compression settings.'),
    ).toBeVisible();
  });

  it('keeps the actual sent HTTP status separate from a failed final result and delivery failure', () => {
    render(
      <RequestDelivery
        result="Failed"
        delivery={normalizeDelivery({
          http_status: 200,
          bytes: 16,
          failed: true,
          failure: {
            ...interrupted,
            category: 'delivery',
            phase: 'write_client',
            code: 'client_write_failed',
          },
        })}
      />,
    );
    expect(screen.getByText('Final result on this site').nextElementSibling).toHaveTextContent(
      'Failed',
    );
    expect(screen.getByText('HTTP status sent by this site').nextElementSibling).toHaveTextContent(
      '200',
    );
    expect(
      screen.getByText('The response could not be written to the client connection'),
    ).toBeVisible();
    expect(screen.getByText(/do not prove that the final client received/)).toBeVisible();
  });

  it('keeps readable observations when independent items are damaged', () => {
    const failure = normalizeFailure({
      ...interrupted,
      causes: [null, { type: '*net.OpError', message: '<script>sample</script>' }],
      correlation: [false, ...interrupted.correlation],
      timings: [{ phase: 'read_response', elapsed_ms: 42 }, { phase: 5 }],
      received_events: 'invalid',
      future_field: { allowed: true },
    });
    render(<FailureDetails failure={failure} />);
    fireEvent.click(screen.getByText('Technical evidence'));
    expect(screen.getByText('sample-upstream-id')).toBeVisible();
    expect(screen.getByText('<script>sample</script>')).toBeVisible();
    expect(document.querySelector('script')).toBeNull();
    expect(screen.getByText(/Some diagnostic items were unreadable/)).toBeVisible();
    expect(screen.getByText('Received events').nextElementSibling).toHaveTextContent(
      'Not recorded',
    );
  });

  it('distinguishes older missing details from unreadable observations', () => {
    const view = render(<FailureDetails />);
    expect(screen.getByText(/No structured failure details were recorded/)).toBeVisible();
    view.rerender(<FailureDetails failure={normalizeFailure('damaged')} />);
    expect(screen.getByText(/diagnostic details could not be read/)).toBeVisible();
    view.rerender(<RequestDelivery result="Failed" />);
    expect(screen.getByText('HTTP status sent by this site').nextElementSibling).toHaveTextContent(
      'Not recorded',
    );
    expect(
      screen.getByText('Delivery observations were not recorded for this request.'),
    ).toBeVisible();
  });
});
