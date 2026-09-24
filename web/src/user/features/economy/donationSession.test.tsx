import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { economyKeys, useCreateDonation } from './queries';
import type { Donation } from './types';

const mocks = vi.hoisted(() => ({ create: vi.fn(), capability: vi.fn(), donations: vi.fn() }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  createDonation: mocks.create,
  getCharityCapability: mocks.capability,
  getDonations: mocks.donations,
}));
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const command = {
  description: 'controlled submission',
  keys: [{ endpointKeyId: '12', expiresAt: null }],
  ownershipAuthorized: true as const,
  discordPublicThanks: false,
};
const donation = { id: '14' } as Donation;
let client: QueryClient;
function login(id: string) {
  const session = { user: { id, username: `fixture-${id}`, effective_level: 1 } };
  const generation = beginManagementSessionRequest(client, 'steward');
  expect(noteManagementSessionSuccess(client, 'steward', session, generation)).toBe(true);
  client.setQueryData(['user', 'session'], session);
}
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  vi.resetAllMocks();
  mocks.capability.mockResolvedValue({ donationIntake: 'open' });
  login('7');
});
afterEach(() => client.clear());

describe('donation commands bind the initiating account', () => {
  it('does not dispatch a queued command after another account logs in', async () => {
    const view = renderHook(useCreateDonation, { wrapper: Wrapper });
    await act(async () => {
      const pending = view.result.current.mutateAsync(command);
      login('8');
      await expect(pending).rejects.toMatchObject({ stationSessionChanged: true });
    });
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.capability).not.toHaveBeenCalled();
    expect(client.getQueryData(['user', 'session'])).toMatchObject({ user: { id: '8' } });
  });

  it.each([200, 401, 403, 409, 503])(
    'ignores a late %s result after switching accounts',
    async (status) => {
      const response = deferred<Donation>();
      mocks.create.mockReturnValue(response.promise);
      const success = vi.fn();
      const error = vi.fn();
      const view = renderHook(useCreateDonation, { wrapper: Wrapper });
      let pending!: Promise<Donation>;
      act(() => {
        pending = view.result.current.mutateAsync(command, { onSuccess: success, onError: error });
      });
      await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce());
      await act(async () => {
        login('8');
        client.setQueryData(economyKeys.charityCapability, { marker: 'new-account' });
        if (status === 200) response.resolve(donation);
        else
          response.reject(
            new ApiError(
              status === 403 ? 'forbidden' : 'request_failed',
              'controlled failure',
              status,
            ),
          );
        await expect(pending).rejects.toMatchObject({ stationSessionChanged: true });
      });
      expect(success).not.toHaveBeenCalled();
      expect(error).not.toHaveBeenCalled();
      expect(mocks.capability).not.toHaveBeenCalled();
      expect(mocks.donations).not.toHaveBeenCalled();
      expect(client.getQueryData(['user', 'session'])).toMatchObject({ user: { id: '8' } });
      expect(client.getQueryData(economyKeys.charityCapability)).toEqual({ marker: 'new-account' });
    },
  );

  it('does not publish a successful command after its reconciliation crosses accounts', async () => {
    const capability = deferred<unknown>();
    mocks.create.mockResolvedValue(donation);
    mocks.capability.mockReturnValue(capability.promise);
    const success = vi.fn();
    const view = renderHook(useCreateDonation, { wrapper: Wrapper });
    let pending!: Promise<Donation>;
    act(() => {
      pending = view.result.current.mutateAsync(command, { onSuccess: success });
    });
    await waitFor(() => expect(mocks.capability).toHaveBeenCalledOnce());
    await act(async () => {
      login('8');
      client.setQueryData(economyKeys.charityCapability, { marker: 'new-account' });
      capability.resolve({ donationIntake: 'closed' });
      await expect(pending).rejects.toMatchObject({ stationSessionChanged: true });
    });
    expect(success).not.toHaveBeenCalled();
    expect(client.getQueryData(economyKeys.charityCapability)).toEqual({ marker: 'new-account' });
    expect(view.result.current.reconcileGeneration).toBe(0);
  });
});
