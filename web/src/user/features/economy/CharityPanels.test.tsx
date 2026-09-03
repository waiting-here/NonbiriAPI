import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import userEn from '../../i18n/en.json';
import { CharityPage } from '../../pages/CharityPage';
import {
  DonationCard,
  DonationComposer,
  DonationIntakePanel,
  DonationKeyOverview,
  DonationKeyPanel,
} from './CharityPanels';
import * as economyQueries from './queries';
import type { CharityCapability, Donation, DonationKey, EndpointKeyChoice } from './types';

const pageMocks = vi.hoisted(() => ({
  usePublicConfig: vi.fn(),
  useUserSession: vi.fn(),
}));

vi.mock('@shared/query/publicConfig', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('@shared/query/publicConfig')>()),
  usePublicConfig: pageMocks.usePublicConfig,
}));

vi.mock('../../data', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('../../data')>()),
  useUserSession: pageMocks.useUserSession,
}));

vi.mock('./queries', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('./queries')>()),
  useCharityCapability: vi.fn(),
  useDonations: vi.fn(),
  useEndpointKeyChoices: vi.fn(),
  useCreateDonation: vi.fn(),
  useEditDonation: vi.fn(),
  useWithdrawDonation: vi.fn(),
  useTerminateDonation: vi.fn(),
}));

const choices: EndpointKeyChoice[] = [
  {
    endpoint: {
      id: '11',
      connectorType: 'openai-compatible',
      baseUrl: 'https://api.example.test/v1',
      origin: { kind: 'custom' },
      note: '',
      enabled: false,
      revision: '1',
      keyCount: '1',
      createdAt: 1_788_100_000,
      updatedAt: 1_788_100_000,
    },
    key: {
      id: '61',
      endpointId: '11',
      displayHead: 'sk-a',
      displayTail: 'tail',
      note: 'existing key',
      enabled: false,
      forceStoreFalse: true,
      suspensionState: 'none',
      revision: '2',
      createdAt: 1_788_100_000,
      updatedAt: 1_788_100_000,
    },
    eligibility: 'eligible',
  },
];

const mainstreamChoice: EndpointKeyChoice = {
  endpoint: {
    ...choices[0].endpoint,
    id: '12',
    origin: {
      kind: 'mainstream',
      channelId: 'mch_abcdefghijklmnopqrstuA',
      name: 'Main channel',
    },
  },
  key: { ...choices[0].key, id: '62', endpointId: '12' },
  eligibility: 'eligible',
};

const otherMainstreamChoice: EndpointKeyChoice = {
  ...mainstreamChoice,
  endpoint: {
    ...mainstreamChoice.endpoint,
    id: '13',
    origin: {
      kind: 'mainstream',
      channelId: 'mch_abcdefghijklmnopqrstvA',
      name: 'Other channel',
    },
  },
  key: { ...mainstreamChoice.key, id: '63', endpointId: '13' },
};

const CHARITY_OPEN: CharityCapability = {
  state: 'available',
  models: [],
  donationIntake: 'open',
};

const charityCopy = userEn.user.charity;

function createMutation(error: ApiError) {
  return {
    data: undefined,
    error: error as unknown,
    isPending: false,
    reconcileGeneration: 0,
    reconcileError: null as unknown,
    isReconciling: false,
    retryReconcile: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    mutateAsync: vi.fn(() => Promise.reject(error)),
  };
}

function successfulMutation() {
  return {
    data: undefined,
    error: null,
    isPending: false,
    reconcileGeneration: 0,
    reconcileError: null as unknown,
    isReconciling: false,
    retryReconcile: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    mutateAsync: vi.fn(() => Promise.resolve(undefined)),
  };
}

describe('donation intake authority', () => {
  it.each(['open', 'closed'] as const)(
    'renders the required %s capability without inference',
    async (state) => {
      const rendered = await renderWithProviders(<DonationIntakePanel state={state} />, {
        station: 'user',
        role: 'user',
      });
      expect(screen.getByText(charityCopy.intakeState[state])).toBeInTheDocument();
      expect(screen.getByText(charityCopy.intakeBody[state])).toBeInTheDocument();
      expect(rendered.container).not.toHaveTextContent('unknown');
    },
  );
});

describe('donation composer recovery', () => {
  beforeEach(() => {
    sessionStorage.clear();
    pageMocks.useUserSession.mockReturnValue({
      data: { user: { id: '7', username: 'owner', effective_level: 1 } },
      error: null,
      isPending: false,
      refetch: vi.fn(),
    });
    pageMocks.usePublicConfig.mockReturnValue({
      data: { announcementEpoch: 'announcement-1' },
    });
  });

  it('keeps the actual page draft namespace and unknown lock independent of announcement epoch', async () => {
    const unknown = createMutation(new ApiError('network_error', 'response lost', 0));
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(unknown as never);
    vi.mocked(economyQueries.useCharityCapability).mockReturnValue({
      data: CHARITY_OPEN,
      error: null,
      isPending: false,
      refetch: vi.fn(),
    } as never);
    vi.mocked(economyQueries.useDonations).mockReturnValue({
      data: [],
      error: null,
      isPending: false,
      isSuccess: true,
      refetch: vi.fn(),
    } as never);
    vi.mocked(economyQueries.useEndpointKeyChoices).mockReturnValue({
      data: choices,
      error: null,
      isPending: false,
      refetch: vi.fn(),
    } as never);
    const rendered = await renderWithProviders(<CharityPage />, {
      station: 'user',
      role: 'user',
    });
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.type(screen.getByRole('textbox'), 'account-scoped draft');
    await rendered.user.click(checkboxes[0]);
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeDisabled();
    expect(sessionStorage.getItem('nonbiri:charity-donation-draft:v1:7')).toBe(
      JSON.stringify({ description: 'account-scoped draft' }),
    );

    pageMocks.usePublicConfig.mockReturnValue({
      data: { announcementEpoch: 'announcement-2' },
    });
    rendered.rerender(<CharityPage />);
    expect(screen.getByRole('textbox')).toHaveValue('account-scoped draft');
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeDisabled();
    expect(unknown.mutateAsync).toHaveBeenCalledTimes(1);

    unknown.reconcileGeneration = 1;
    rendered.rerender(<CharityPage />);
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeEnabled();
    expect(screen.getByText(charityCopy.mutationReconciled)).toBeInTheDocument();
  });

  it('preserves only the description and requires key/ownership reconfirmation after 409', async () => {
    const conflict = createMutation(new ApiError('conflict', 'revision changed', 409));
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(conflict as never);
    const rendered = await renderWithProviders(
      <DonationComposer choices={choices} draftNamespace="account-7" />,
      { station: 'user', role: 'user' },
    );
    const user = rendered.user;
    const description = screen.getByRole('textbox');
    const checkboxes = screen.getAllByRole('checkbox');
    await user.type(description, 'safe description');
    await user.click(checkboxes[0]);
    await user.click(checkboxes[1]);
    await user.click(screen.getByRole('button'));
    expect(conflict.mutateAsync).toHaveBeenCalledTimes(1);
    expect(description).toHaveValue('safe description');
    expect(checkboxes[0]).not.toBeChecked();
    expect(checkboxes[1]).not.toBeChecked();
    expect(sessionStorage.getItem('nonbiri:charity-donation-draft:v1:account-7')).toBe(
      JSON.stringify({ description: 'safe description' }),
    );
    expect(JSON.stringify(sessionStorage)).not.toMatch(/61|ownership|secret/i);
  });

  it('keeps an unknown create locked across an unrelated announcement epoch and unlocks on a same-value GET generation', async () => {
    const unknown = createMutation(new ApiError('network_error', 'response lost', 0));
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(unknown as never);
    const Host = ({ announcementEpoch }: { announcementEpoch: string }) => (
      <section data-announcement-epoch={announcementEpoch}>
        <DonationComposer choices={choices} draftNamespace="account-8" />
      </section>
    );
    const rendered = await renderWithProviders(<Host announcementEpoch="announcement-1" />, {
      station: 'user',
      role: 'user',
    });
    const user = rendered.user;
    const checkboxes = screen.getAllByRole('checkbox');
    await user.type(screen.getByRole('textbox'), 'one intent');
    await user.click(checkboxes[0]);
    await user.click(checkboxes[1]);
    const submit = screen.getByRole('button');
    await user.click(submit);
    expect(submit).toBeDisabled();
    rendered.rerender(<Host announcementEpoch="announcement-2" />);
    expect(screen.getByRole('textbox')).toHaveValue('one intent');
    expect(submit).toBeDisabled();
    await user.click(submit);
    expect(unknown.mutateAsync).toHaveBeenCalledTimes(1);
    unknown.reconcileGeneration = 1;
    rendered.rerender(<Host announcementEpoch="announcement-2" />);
    expect(screen.getByText(charityCopy.mutationReconciled)).toBeInTheDocument();
    expect(submit).toBeEnabled();
    const refreshedCheckboxes = screen.getAllByRole('checkbox');
    await user.click(refreshedCheckboxes[0]);
    await user.click(refreshedCheckboxes[1]);
    await user.click(screen.getByRole('button'));
    expect(unknown.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it('keeps create blocked on a failed authority GET and retries only the GET', async () => {
    const unknown = createMutation(new ApiError('network_error', 'response lost', 0));
    unknown.reconcileError = new ApiError('network_error', 'refresh failed', 0);
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(unknown as never);
    const rendered = await renderWithProviders(
      <DonationComposer choices={choices} draftNamespace="account-8b" />,
      { station: 'user', role: 'user' },
    );
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.type(screen.getByRole('textbox'), 'one intent');
    await rendered.user.click(checkboxes[0]);
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: /retry/i }));
    expect(unknown.retryReconcile).toHaveBeenCalledTimes(1);
    expect(unknown.mutateAsync).toHaveBeenCalledTimes(1);

    unknown.reconcileError = null;
    unknown.reconcileGeneration = 1;
    rendered.rerender(<DonationComposer choices={choices} draftNamespace="account-8b" />);
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeEnabled();
    expect(screen.getByText(charityCopy.mutationReconciled)).toBeInTheDocument();
  });

  it('allows the canonical empty description and never offers a second endpoint or secret form', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      <DonationComposer choices={choices} draftNamespace="account-9" />,
      { station: 'user', role: 'user' },
    );
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.click(checkboxes[0]);
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(screen.getByRole('button'));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith({
        description: '',
        keys: [{ endpointKeyId: '61', expiresAt: null }],
        ownershipAuthorized: true,
      }),
    );
    expect(rendered.container.querySelector('input[type="password"]')).toBeNull();
    expect(rendered.container.querySelector('input[type="url"]')).toBeNull();
  });

  it('preserves the safe draft while an ineligible cross-page choice becomes eligible', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    sessionStorage.setItem(
      'nonbiri:charity-donation-draft:v1:account-10',
      JSON.stringify({ description: 'return-safe draft' }),
    );
    const rendered = await renderWithProviders(
      <DonationComposer
        choices={[{ ...choices[0], eligibility: 'already_donated' }]}
        draftNamespace="account-10"
      />,
      { station: 'user', role: 'user' },
    );
    expect(screen.getByRole('textbox')).toHaveValue('return-safe draft');
    expect(screen.getByRole('link')).toHaveAttribute('href', '/endpoints');
    rendered.rerender(<DonationComposer choices={choices} draftNamespace="account-10" />);
    expect(screen.getByRole('textbox')).toHaveValue('return-safe draft');
    expect(rendered.container.querySelector('input[type="password"]')).toBeNull();
  });

  it('requires a pure mainstream channel or a custom-only selection', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      <DonationComposer
        choices={[choices[0], mainstreamChoice]}
        draftNamespace="account-mainstream-mixed"
      />,
      { station: 'user', role: 'user' },
    );
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.click(checkboxes[0]);
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(checkboxes[2]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('blocks a cross-channel mainstream selection before submission', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      <DonationComposer
        choices={[mainstreamChoice, otherMainstreamChoice]}
        draftNamespace="account-mainstream-cross-channel"
      />,
      { station: 'user', role: 'user' },
    );
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.click(checkboxes[0]);
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(checkboxes[2]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('submits one expiry per selected key as UTC Unix seconds', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      <DonationComposer choices={[choices[0]]} draftNamespace="account-key-expiry" />,
      { station: 'user', role: 'user' },
    );
    const checkboxes = screen.getAllByRole('checkbox');
    await rendered.user.click(checkboxes[0]);
    const expiry = rendered.container.querySelector('input[type="datetime-local"]');
    if (!expiry) throw new Error('expiry control not rendered');
    await rendered.user.type(expiry, '2030-01-01T00:00');
    expect(checkboxes[0]).toBeChecked();
    await rendered.user.click(checkboxes[1]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith({
        description: '',
        keys: [{ endpointKeyId: '61', expiresAt: 1_893_456_000 }],
        ownershipAuthorized: true,
      }),
    );
  });

  it('shows the account-level key overview with local blocked and ended filters', async () => {
    const key = {
      id: '51',
      endpointKeyId: '61',
      displayHead: 'sk-head',
      displayTail: 'available-tail',
      source: {
        kind: 'custom' as const,
        baseUrl: 'https://safe.example.test/v1',
        connectorType: 'openai-compatible',
      },
      physicalEnabled: true,
      charityState: 'available' as const,
      limits: { price: '5', calls: '6', tokens: '7' },
      usage: {
        priceUsed: '1',
        priceInflight: '2',
        callsUsed: '3',
        callsInflight: '4',
        tokensUsed: '5',
        tokensInflight: '6',
      },
      tokenReserve: 8,
      expiresAt: 1_900_000_000,
      streak: { generation: '2', count: '3', failureDisabled: false },
      endedReason: null,
    } satisfies DonationKey;
    const blocked = {
      ...key,
      id: '52',
      displayTail: 'blocked-tail',
      physicalEnabled: false,
      charityState: 'disabled' as const,
      streak: { ...key.streak, failureDisabled: true },
    };
    const ended = {
      ...key,
      id: '53',
      displayTail: 'ended-tail',
      charityState: 'ended' as const,
      expiresAt: null,
      endedReason: 'withdrawn' as const,
    };
    const donation: Donation = {
      id: '41',
      status: 'approved',
      revision: '9',
      description: 'overview donation',
      reviewResult: { decision: 'approve', reason: 'ok', reviewedAt: 1_788_100_005 },
      keys: [key, blocked, ended],
      createdAt: 1_788_100_000,
      updatedAt: 1_788_100_010,
    };
    const rendered = await renderWithProviders(<DonationKeyOverview donations={[donation]} />, {
      station: 'user',
      role: 'user',
    });
    expect(rendered.container.innerHTML).not.toContain('61');
    expect(screen.getByText('sk-head…available-tail')).toBeInTheDocument();
    expect(screen.getByText('sk-head…blocked-tail')).toBeInTheDocument();
    expect(screen.getByText('sk-head…ended-tail')).toBeInTheDocument();
    await rendered.user.selectOptions(screen.getByRole('combobox'), 'blocked');
    expect(screen.queryByText('sk-head…available-tail')).not.toBeInTheDocument();
    expect(screen.getByText('sk-head…blocked-tail')).toBeInTheDocument();
    await rendered.user.selectOptions(screen.getByRole('combobox'), 'ended');
    expect(screen.queryByText('sk-head…blocked-tail')).not.toBeInTheDocument();
    expect(screen.getByText('sk-head…ended-tail')).toBeInTheDocument();
  });

  it('keeps physical, failure, and each exhausted dimension visibly separate', async () => {
    const key: DonationKey = {
      id: '51',
      endpointKeyId: '61',
      displayHead: 'sk-head',
      displayTail: 'tail',
      source: {
        kind: 'custom',
        baseUrl: 'https://api.example.test/v1',
        connectorType: 'openai-compatible',
      },
      physicalEnabled: false,
      charityState: 'disabled',
      limits: { price: null, calls: null, tokens: null },
      usage: {
        priceUsed: '0',
        priceInflight: '0',
        callsUsed: '0',
        callsInflight: '0',
        tokensUsed: '0',
        tokensInflight: '0',
      },
      tokenReserve: 0,
      expiresAt: null,
      streak: { generation: '1', count: '10', failureDisabled: true },
      endedReason: null,
    };
    const rendered = await renderWithProviders(<DonationKeyPanel donationKey={key} />, {
      station: 'user',
      role: 'user',
    });
    expect(screen.getByText(charityCopy.keyState.physical_disabled)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.failure_disabled)).toBeInTheDocument();
    rendered.rerender(
      <DonationKeyPanel
        donationKey={{
          ...key,
          physicalEnabled: true,
          charityState: 'exhausted',
          limits: { price: '0', calls: '0', tokens: '0' },
          streak: { generation: '1', count: '0', failureDisabled: false },
        }}
      />,
    );
    expect(screen.getByText(charityCopy.keyState.price_exhausted)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.calls_exhausted)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.tokens_exhausted)).toBeInTheDocument();
  });

  it.each([
    ['list card', true],
    ['detail card', false],
  ])(
    'unlocks a 409 edit on a same-value authority GET in the %s',
    async (_label, showDetailLink) => {
      const conflict = createMutation(new ApiError('conflict', 'revision changed', 409));
      conflict.reconcileError = new ApiError('network_error', 'refresh failed', 0);
      vi.mocked(economyQueries.useEditDonation).mockReturnValue(conflict as never);
      vi.mocked(economyQueries.useWithdrawDonation).mockReturnValue(successfulMutation() as never);
      vi.mocked(economyQueries.useTerminateDonation).mockReturnValue(successfulMutation() as never);
      const donation: Donation = {
        id: '41',
        status: 'pending',
        revision: '9',
        description: 'original draft',
        reviewResult: null,
        keys: [],
        createdAt: 1_788_100_000,
        updatedAt: 1_788_100_010,
      };
      const rendered = await renderWithProviders(
        <DonationCard donation={donation} showDetailLink={showDetailLink} />,
        { station: 'user', role: 'user' },
      );
      await rendered.user.click(screen.getByRole('button', { name: /edit/i }));
      const description = screen.getByRole('textbox');
      await rendered.user.clear(description);
      await rendered.user.type(description, 'preserved safe edit');
      await rendered.user.click(screen.getByRole('button', { name: /save/i }));
      expect(conflict.mutateAsync).toHaveBeenCalledWith({
        id: '41',
        description: 'preserved safe edit',
        expectedRevision: '9',
      });
      expect(screen.getByRole('button', { name: /working/i })).toBeDisabled();
      await rendered.user.click(screen.getByRole('button', { name: /retry/i }));
      expect(conflict.retryReconcile).toHaveBeenCalledTimes(1);
      expect(conflict.mutateAsync).toHaveBeenCalledTimes(1);
      conflict.reconcileGeneration = 1;
      conflict.reconcileError = null;
      rendered.rerender(<DonationCard donation={donation} showDetailLink={showDetailLink} />);
      expect(screen.getByRole('textbox')).toHaveValue('preserved safe edit');
      expect(screen.getByRole('button', { name: /save/i })).toBeEnabled();
      expect(screen.getByText(charityCopy.mutationReconciled)).toBeInTheDocument();
    },
  );

  it.each([
    ['withdraw', 'pending'],
    ['terminate', 'approved'],
  ] as const)(
    'keeps %s locked when its GET fails and unlocks on a same-value successful GET',
    async (operation, status) => {
      const rejected = createMutation(new ApiError('network_error', 'response lost', 0));
      rejected.reconcileError = new ApiError('network_error', 'refresh failed', 0);
      vi.mocked(economyQueries.useEditDonation).mockReturnValue(successfulMutation() as never);
      vi.mocked(economyQueries.useWithdrawDonation).mockReturnValue(
        (operation === 'withdraw' ? rejected : successfulMutation()) as never,
      );
      vi.mocked(economyQueries.useTerminateDonation).mockReturnValue(
        (operation === 'terminate' ? rejected : successfulMutation()) as never,
      );
      const donation: Donation = {
        id: operation === 'withdraw' ? '42' : '43',
        status,
        revision: '9',
        description: 'unchanged authority',
        reviewResult: null,
        keys: [],
        createdAt: 1_788_100_000,
        updatedAt: 1_788_100_010,
      };
      const rendered = await renderWithProviders(<DonationCard donation={donation} />, {
        station: 'user',
        role: 'user',
      });
      const operationLabel = charityCopy[operation];
      await rendered.user.click(screen.getByRole('button', { name: operationLabel }));
      const operationButtons = screen.getAllByRole('button', {
        name: operationLabel,
      });
      await rendered.user.click(operationButtons[operationButtons.length - 1]);
      await waitFor(() => expect(rejected.mutateAsync).toHaveBeenCalledTimes(1));
      await waitFor(() =>
        expect(screen.getByRole('button', { name: operationLabel })).toBeDisabled(),
      );
      await rendered.user.click(screen.getByRole('button', { name: /retry/i }));
      expect(rejected.retryReconcile).toHaveBeenCalledTimes(1);
      expect(rejected.mutateAsync).toHaveBeenCalledTimes(1);

      rejected.reconcileGeneration = 1;
      rejected.reconcileError = null;
      rendered.rerender(<DonationCard donation={donation} />);
      expect(screen.getByRole('button', { name: operationLabel })).toBeEnabled();
      expect(screen.getByText(charityCopy.mutationReconciled)).toBeInTheDocument();
    },
  );
});
