import { fireEvent, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { MainstreamEntries, type MainstreamLayout } from './MainstreamEntries';

function Editor({
  failed = false,
  connector = 'openai-compatible',
}: {
  failed?: boolean;
  connector?: string;
}) {
  const [value, setValue] = useState<MainstreamLayout>({
    entries: [
      {
        id: '1',
        name: 'Primary',
        connector_type: connector,
        base_url: 'https://example.test/v1',
        native_services: ['chat_completions', 'rerank'],
        rerank_format: 'cohere',
        enabled: true,
      },
    ],
    discovery_source: null,
    model_mappings: [],
  });
  return (
    <form onSubmit={(event) => event.preventDefault()}>
      <MainstreamEntries
        value={value}
        onChange={setValue}
        showErrors={failed}
        disabled={false}
        connectorLabels={{ 'openai-compatible': 'common.connectorChatCompletions' }}
        options={[
          {
            connector_type: 'openai-chat-completions',
            configuration_type: 'openai-compatible',
            native_services: ['chat_completions'],
            default_native_services: ['chat_completions'],
            rerank_formats: [],
          },
          {
            connector_type: 'openai-compatible',
            configuration_type: 'openai-compatible',
            native_services: ['chat_completions', 'rerank'],
            default_native_services: ['chat_completions'],
            rerank_formats: ['generic', 'cohere'],
          },
        ]}
      />
      <output aria-label="Saved connectors">
        {value.entries.map((entry) => entry.connector_type).join(', ')}
      </output>
      <button type="submit">Save channel</button>
    </form>
  );
}

describe('channel entry disclosures', () => {
  it('shows protocol and custom rerank format while collapsed and reveals an invalid hidden URL', async () => {
    const view = await renderWithProviders(<Editor />, { station: 'admin', role: 'admin' });
    const summary = screen.getByText('Primary').closest('summary')!;
    expect(summary).toHaveTextContent('OpenAI Chat Completions');
    expect(summary).toHaveTextContent('Cohere');
    await view.user.click(summary);
    const input = screen.getByLabelText('Endpoint URL');
    await view.user.clear(input);
    await view.user.type(input, 'invalid-url');
    await view.user.click(summary);
    expect(summary.closest('details')).not.toHaveAttribute('open');
    await view.user.click(screen.getByRole('button', { name: 'Save channel' }));
    await waitFor(() => expect(summary.closest('details')).toHaveAttribute('open'));
    await waitFor(() => expect(input).toHaveFocus());
    expect(input).toHaveValue('invalid-url');
    expect(screen.queryByLabelText('Custom rerank path')).not.toBeInTheDocument();
  });

  it('keeps an existing connector and defaults new entries to the registered configuration protocol', async () => {
    const view = await renderWithProviders(<Editor connector="openai-chat-completions" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(screen.getByText('Primary').closest('summary')!);
    const protocol = screen.getByLabelText('Protocol');
    expect(protocol).toHaveValue('openai-compatible');
    expect(protocol.querySelectorAll('option')).toHaveLength(1);
    await view.user.type(screen.getByLabelText('Endpoint URL'), '/changed');
    await view.user.click(screen.getByRole('button', { name: 'Add endpoint' }));
    expect(screen.getByLabelText('Saved connectors')).toHaveTextContent(
      'openai-chat-completions, openai-compatible',
    );
  });

  it('reopens an unsaved entry after a failed save without replacing its draft', async () => {
    const view = await renderWithProviders(<Editor />, { station: 'admin', role: 'admin' });
    const summary = screen.getByText('Primary').closest('summary')!;
    await view.user.click(summary);
    fireEvent.change(screen.getByLabelText('Endpoint URL'), {
      target: { value: 'https://changed.test/v2' },
    });
    await view.user.click(summary);
    view.rerender(<Editor failed />);
    await waitFor(() => expect(summary.closest('details')).toHaveAttribute('open'));
    expect(screen.getByLabelText('Endpoint URL')).toHaveValue('https://changed.test/v2');
  });
});
