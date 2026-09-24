import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders, assertNoSensitiveQueryCache } from '../../../../test/unit/support';
import {
  beginManagementSessionRequest,
  clearStationSession,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { modelFixture, taskFixture } from '@shared/picturebook/fixtures';
import { ModelForm } from './ModelForm';
import { ImageResults } from './ImageResults';
import { ImagePolicyNotice } from './ImagePolicyNotice';

const wallet = { general: '0', sketch_paper: '100', sketch_brush: '100' };
const session = { user: { id: '1', username: 'fixture-user', effective_level: 1 } };
function reply(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
}

describe('picture book page state', () => {
  it('quotes per-image combined prices, escapes model copy, and retries one uncertain submission without caching its prompt', async () => {
    const calls: { body: string; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: unknown, init?: RequestInit) => {
        calls.push({
          body: String(init?.body),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        if (calls.length === 1) throw new TypeError('connection lost');
        return reply({ task: taskFixture() });
      }),
    );
    const accepted = vi.fn();
    const view = await renderWithProviders(
      <ModelForm
        account="1"
        models={[modelFixture()]}
        available
        wallet={wallet}
        onAccepted={accepted}
      />,
      { station: 'user', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], session);
    expect(view.container.querySelector('script')).toBeNull();
    expect(view.container.querySelector('img')).toBeNull();
    await view.user.type(screen.getByLabelText(/Prompt \*/), 'synthetic-private-prompt-marker');
    await view.user.clear(screen.getByLabelText('Image count'));
    await view.user.type(screen.getByLabelText('Image count'), '4');
    expect(screen.getByText('8 paper + 4 brushes')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Reserve currency and join queue' }));
    await screen.findByText(/submission result is unconfirmed/i);
    expect(screen.getByLabelText(/Prompt \*/)).toBeDisabled();
    expect(screen.getByLabelText('Image model')).toBeDisabled();
    assertNoSensitiveQueryCache(view.queryClient, ['synthetic-private-prompt-marker']);
    act(() => {
      noteManagementSessionSuccess(
        view.queryClient,
        'steward',
        session,
        beginManagementSessionRequest(view.queryClient, 'steward'),
      );
    });
    const localSet = vi.spyOn(Storage.prototype, 'setItem');
    await view.user.click(screen.getByRole('button', { name: 'Retry the same submission' }));
    await waitFor(() => expect(accepted).toHaveBeenCalledOnce());
    expect(calls).toHaveLength(2);
    expect(calls[0]).toEqual(calls[1]);
    expect(JSON.parse(calls[0].body)).toMatchObject({ expected_model_revision: '2', n: 4 });
    expect(localSet).not.toHaveBeenCalled();
    assertNoSensitiveQueryCache(view.queryClient, ['synthetic-private-prompt-marker']);
  });
  it('does not silently replace a quoted model revision or enable submission with insufficient balances', async () => {
    const model = modelFixture();
    const view = await renderWithProviders(
      <ModelForm
        account="1"
        models={[model]}
        available
        wallet={{ ...wallet, sketch_brush: '0' }}
        onAccepted={() => undefined}
      />,
      { station: 'user' },
    );
    expect(screen.getByRole('button', { name: 'Reserve currency and join queue' })).toBeDisabled();
    view.rerender(
      <ModelForm
        account="1"
        models={[{ ...model, revision: '3', price: { paper: '5', brush: '2' } }]}
        available
        wallet={wallet}
        onAccepted={() => undefined}
      />,
    );
    expect(screen.getByText(/model configuration changed/i)).toBeVisible();
    expect(screen.getAllByText('2 paper + 1 brushes').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Reserve currency and join queue' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Load latest configuration' }));
    expect(screen.getAllByText('5 paper + 2 brushes').length).toBeGreaterThan(0);
  });
  it('renders independent size controls and submits their canonical size string', async () => {
    const model = modelFixture();
    model.parameters.push({
      key: 'size',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
      default: '80x144',
      dimensions: {
        format: 'width_height',
        width: { minimum: 48, maximum: 240, step: 16 },
        height: { minimum: 80, maximum: 272, step: 32 },
      },
    });
    const writes: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path: unknown, init?: RequestInit) => {
        writes.push(String(init?.body));
        return reply({ task: taskFixture() });
      }),
    );
    const accepted = vi.fn();
    const view = await renderWithProviders(
      <ModelForm account="1" models={[model]} available wallet={wallet} onAccepted={accepted} />,
      { station: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], session);
    expect(screen.getByLabelText('Width')).toHaveValue(80);
    expect(screen.getByLabelText('Height')).toHaveAttribute('step', '32');
    await view.user.type(screen.getByLabelText(/Prompt \*/), 'dimension fixture');
    await view.user.clear(screen.getByLabelText('Width'));
    await view.user.type(screen.getByLabelText('Width'), '112');
    await view.user.clear(screen.getByLabelText('Height'));
    await view.user.type(screen.getByLabelText('Height'), '176');
    await view.user.click(screen.getByRole('button', { name: 'Reserve currency and join queue' }));
    await waitFor(() => expect(accepted).toHaveBeenCalledOnce());
    expect(JSON.parse(writes[0])).toMatchObject({ size: '112x176' });
    expect(JSON.parse(writes[0])).not.toHaveProperty('width');
  });
  it('rejects a late accepted response after the session is replaced', async () => {
    let finish!: (reply: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      ),
    );
    const accepted = vi.fn();
    const view = await renderWithProviders(
      <ModelForm
        account="1"
        models={[modelFixture()]}
        available
        wallet={wallet}
        onAccepted={accepted}
      />,
      { station: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], session);
    await view.user.type(screen.getByLabelText(/Prompt \*/), 'synthetic-delayed-prompt');
    await view.user.click(screen.getByRole('button', { name: 'Reserve currency and join queue' }));
    act(() => {
      clearStationSession(view.queryClient, 'steward');
      noteManagementSessionSuccess(
        view.queryClient,
        'steward',
        {
          user: { id: '2', username: 'another-fixture', effective_level: 1 },
        },
        beginManagementSessionRequest(view.queryClient, 'steward'),
      );
    });
    await act(async () => finish(reply({ task: taskFixture() })));
    expect(accepted).not.toHaveBeenCalled();
  });
  it('keeps collected images only in page memory, preserves their downloads after server expiry, and revokes them on unmount', async () => {
    const revoke = vi.fn();
    vi.stubGlobal(
      'URL',
      class extends URL {
        static createObjectURL = vi.fn(() => 'blob:http://user.test/fixture');
        static revokeObjectURL = revoke;
      },
    );
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(new Uint8Array([1, 2, 3]), {
            headers: { 'Content-Type': 'image/png', 'Content-Length': '3' },
          }),
      ),
    );
    const task = taskFixture({
      status: 'succeeded',
      billing_state: 'charged',
      actual_images: 1,
      result_available: true,
      result_expires_at: 1700000610,
      images: [{ index: 0, mime: 'image/png', bytes: 3 }],
    });
    const view = await renderWithProviders(<ImageResults task={task} account="1" />, {
      station: 'user',
    });
    view.queryClient.setQueryData(['user', 'session'], session);
    await view.user.click(
      await screen.findByRole('button', { name: 'Collect and preview originals' }),
    );
    await screen.findByRole('link', { name: 'Download original' });
    expect(screen.getByRole('img')).toHaveAttribute('src', 'blob:http://user.test/fixture');
    view.rerender(
      <ImageResults task={{ ...task, result_available: false, images: [] }} account="1" />,
    );
    expect(screen.getByRole('link', { name: 'Download original' })).toBeVisible();
    expect(revoke).not.toHaveBeenCalled();
    view.unmount();
    expect(revoke).toHaveBeenCalledWith('blob:http://user.test/fixture');
  });
  it('explains full-charge partial success and the collection window in Chinese', async () => {
    await renderWithProviders(<ImagePolicyNotice />, { station: 'user', locale: 'zh' });
    expect(screen.getByText(/至少生成一张有效图片/)).toBeVisible();
    expect(screen.getByText(/10分钟可领取/)).toBeVisible();
  });
});
