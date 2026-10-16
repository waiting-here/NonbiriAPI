import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders, scanQueryClientForTokens } from '../../../../test/unit/support';
import { MfaChallenge } from './MfaChallenge';

vi.mock('qrcode', () => ({ toCanvas: vi.fn(async () => undefined) }));
const secret = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP';
const status = { enrolled: false, required: true, verified: false, verified_until: null };
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
const failure = (code: string, status: number) =>
  json({ error: { code, message: 'Synthetic error' } }, status);

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('account authenticator challenge', () => {
  it('binds locally, clears rejected codes, and keeps enrollment material out of query caches', async () => {
    const onSuccess = vi.fn();
    let attempt = 0;
    const requests: { path: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string, options?: RequestInit) => {
        const body = options?.body ? JSON.parse(String(options.body)) : undefined;
        requests.push({ path: input, body });
        if (input.endsWith('/setup'))
          return json({
            secret,
            otpauth_uri: `otpauth://totp/Fixture?secret=${secret}`,
            expires_at: Math.floor(Date.now() / 1000) + 600,
          });
        if (input.endsWith('/confirm'))
          return ++attempt === 1
            ? failure('mfa_invalid_code', 403)
            : json({ ...status, enrolled: true, verified: true, verified_until: 1800000000 });
        return json(status);
      }),
    );
    const view = await renderWithProviders(<MfaChallenge onSuccess={onSuccess} />, {
      station: 'user',
    });
    await screen.findByRole('img', { name: 'Authenticator setup QR code' });
    await view.user.click(screen.getByText('Enter a setup key instead'));
    expect(screen.getByLabelText('Setup key')).toHaveValue(secret);
    expect(scanQueryClientForTokens(view.queryClient, [secret]).hitSurfaces).toEqual([]);
    await view.user.type(screen.getByLabelText('Authenticator code'), '123456');
    await view.user.click(screen.getByRole('button', { name: 'Finish setup' }));
    await screen.findByText('That code is incorrect or has already been used. Try the next code.');
    expect(screen.getByLabelText('Authenticator code')).toHaveValue('');
    expect(onSuccess).not.toHaveBeenCalled();
    await view.user.type(screen.getByLabelText('Authenticator code'), '654321');
    await view.user.click(screen.getByRole('button', { name: 'Finish setup' }));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce());
    expect(screen.queryByLabelText('Setup key')).not.toBeInTheDocument();
    expect(
      requests
        .filter((request) => request.path.endsWith('/confirm'))
        .map((request) => request.body),
    ).toEqual([{ code: '123456' }, { code: '654321' }]);
  });

  it('removes expired setup material and provides a fresh setup path', async () => {
    let setups = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string) =>
        input.endsWith('/setup')
          ? json({
              secret,
              otpauth_uri: `otpauth://totp/Fixture?secret=${secret}`,
              expires_at: Math.ceil(Date.now() / 1000) + (++setups === 1 ? 1 : 600),
            })
          : json(status),
      ),
    );
    const view = await renderWithProviders(<MfaChallenge onSuccess={vi.fn()} />, {
      station: 'user',
    });
    await screen.findByRole('img');
    await waitFor(() => expect(screen.queryByRole('img')).not.toBeInTheDocument(), {
      timeout: 2500,
    });
    expect(screen.queryByLabelText('Setup key')).not.toBeInTheDocument();
    expect(
      screen.getByText('This setup has expired. Start again to get a new QR code and key.'),
    ).toBeVisible();
    vi.useRealTimers();
    await view.user.click(screen.getByRole('button', { name: 'Check status and try again' }));
    await screen.findByRole('img');
  });

  it('requires a fresh code to turn off an optional authenticator', async () => {
    const onSuccess = vi.fn();
    const fetch = vi.fn(async (input: string) =>
      input.endsWith('/disable')
        ? json({ ...status, required: false })
        : json({ ...status, enrolled: true, required: false, verified: true }),
    );
    vi.stubGlobal('fetch', fetch);
    const view = await renderWithProviders(
      <MfaChallenge mode="disable" dialog onCancel={vi.fn()} onSuccess={onSuccess} />,
      { station: 'user' },
    );
    const input = await screen.findByLabelText('Authenticator code');
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Turn off authenticator',
      }),
    ).toBeDisabled();
    fireEvent.change(input, { target: { value: '654321' } });
    await view.user.click(screen.getByRole('button', { name: 'Turn off authenticator' }));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce());
    expect(fetch.mock.calls.some(([path]) => path.endsWith('/disable'))).toBe(true);
  });

  it('does not offer disable when the current level requires it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ ...status, enrolled: true, verified: true })),
    );
    await renderWithProviders(
      <MfaChallenge mode="disable" dialog onCancel={vi.fn()} onSuccess={vi.fn()} />,
      { station: 'user' },
    );
    await screen.findByText(
      'Your current account level requires an authenticator. You cannot turn it off.',
    );
    expect(screen.queryByLabelText('Authenticator code')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Turn off authenticator' })).toBeDisabled();
  });
});
