import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import type { BindingGuide } from '@shared/operations/charity';
import type { NativeService } from '@shared/nativeServices';
import { BindingServiceEditor } from './BindingRouteGuide';

it('shows a paused direct service before it is selected and keeps the configured fallback editable', async () => {
  const guide: BindingGuide = {
    interfaces: ['chat_completions'],
    services: [
      { native_service: 'chat_completions', state: 'failure_disabled' },
      { native_service: 'responses', state: 'available' },
    ],
    routes: [
      { operation: 'chat_completions', native_service: 'chat_completions', mode: 'native' },
      { operation: 'chat_completions', native_service: 'responses', mode: 'converted' },
    ],
  };
  const onChange = vi.fn();
  const editor = (value: NativeService[]) => (
    <BindingServiceEditor
      guide={guide}
      options={['chat_completions', 'responses']}
      value={value}
      onChange={onChange}
    />
  );
  const view = await renderWithProviders(editor(['responses']), {
    station: 'admin',
    role: 'admin',
    locale: 'en',
  });
  await view.user.click(screen.getByText('Adjust upstream services'));
  const chat = screen.getByRole('checkbox', { name: 'Chat completions' });
  expect(chat).toHaveAccessibleDescription(
    /Paused after consecutive failures.*Direct for Chat completions/,
  );
  expect(chat).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Responses' })).toHaveAccessibleDescription(
    'Via conversion for Chat completions',
  );
  await view.user.click(screen.getByText('Paused after consecutive failures'));
  expect(onChange).toHaveBeenCalledWith(['chat_completions', 'responses']);
  view.rerender(editor(['chat_completions']));
  expect(screen.getByText('Review service health (1)')).toBeVisible();
  view.rerender(editor([]));
  expect(screen.getByRole('alert')).toHaveTextContent('Select an upstream service');
});
