import { ApiError } from '@shared/query/http';
import { screen, waitFor, within } from '@testing-library/react';
import { type ReactElement, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import userEn from '../../i18n/en.json';
import { CharityPage } from '../../pages/CharityPage';
import * as catalogModule from './catalog';
import {
  CharityCapabilityPanel,
  DonationCard,
  DonationComposer,
  DonationIntakePanel,
  DonationKeyOverview,
  DonationKeyPanel,
} from './CharityPanels';
import * as economyQueries from './queries';
import type { CharityCapability, Donation, DonationKey, EndpointKeyChoice } from './types';

installNativeDialog();

afterEach(() => {
  vi.unstubAllGlobals();
});

const pageMocks = vi.hoisted(() => ({
  usePublicConfig: vi.fn(),
  useUserSession: vi.fn(),
}));

const pickerHarness = vi.hoisted(() => ({
  choices: [] as unknown[],
  readBlocked: false,
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
  useCreateDonation: vi.fn(),
  useEditDonation: vi.fn(),
  useWithdrawDonation: vi.fn(),
  useTerminateDonation: vi.fn(),
}));

vi.mock('./catalog', async (loadOriginal) => ({
  ...(await loadOriginal<typeof import('./catalog')>()),
  useCharityCatalog: vi.fn(),
}));

type PickerHarnessProps = {
  accountId: string;
  selected: readonly EndpointKeyChoice[];
  onChange: (choices: EndpointKeyChoice[]) => void;
  disabled?: boolean;
  enabled?: boolean;
  renderSelected?: (choice: EndpointKeyChoice) => ReactNode;
  onReadStateChange?: (blocked: boolean) => void;
};

vi.mock('./DonationResourcePicker', async () => {
  const React = await import('react');
  function TestDonationResourcePicker({
    selected,
    onChange,
    disabled = false,
    enabled = true,
    renderSelected,
    onReadStateChange,
  }: PickerHarnessProps) {
    const available = pickerHarness.choices as readonly EndpointKeyChoice[];
    const readBlocked = pickerHarness.readBlocked;
    React.useEffect(() => {
      onReadStateChange?.(readBlocked || !enabled);
    }, [enabled, onReadStateChange, readBlocked]);
    return (
      <section data-testid="donation-resource-picker">
        {available.map((choice) => {
          const checked = selected.some((item) => item.key.id === choice.key.id);
          const rowDisabled = disabled || !enabled || choice.eligibility !== 'eligible';
          return (
            <label key={choice.key.id}>
              <input
                type="checkbox"
                aria-label={`Select resource key ${choice.key.id}`}
                checked={checked}
                disabled={rowDisabled}
                onChange={(event) => {
                  if (event.currentTarget.checked) {
                    onChange([...selected, choice]);
                  } else {
                    onChange(selected.filter((item) => item.key.id !== choice.key.id));
                  }
                }}
              />
              <span>{`Resource ${choice.key.id}`}</span>
            </label>
          );
        })}
        <div data-testid="donation-resource-picker-selected">
          {selected.map((choice) => (
            <div key={choice.key.id}>{renderSelected?.(choice)}</div>
          ))}
        </div>
      </section>
    );
  }
  return { DonationResourcePicker: TestDonationResourcePicker };
});

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
      nativeServices: ['chat_completions', 'responses'],
      credentialId: '81',
      credentialVersion: '1',
      grantRevision: '1',
      discoverySourceId: null,
      endpointId: '11',
      displayHead: 'sk-a',
      displayTail: 'tail',
      note: 'existing key',
      enabled: false,
      forceStoreFalse: true,
      maxConcurrency: 0,
      maxRPM: 0,
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
  models: [
    {
      recurringDiscount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
      effectiveDiscount: { source: 'fixed', percent: 80, next_change_at: null },

      modelTypes: ['chat_completions', 'embeddings'],
      id: '7',
      provider: 'provider',
      model: 'charity-model',
      fullName: '[公益]provider/charity-model',
      pricing: {
        currentUserPriceMilli: '2400',

        mode: 'per_request',
        userPriceMilli: '3000',
        discountedUserPriceMilli: '2400',
      },
      discount: {
        enabled: true,
        percent: 80,
        startAt: 1_788_099_000,
        endAt: 1_788_101_000,
      },
    },
  ],
  donationIntake: 'open',
  serverNow: 1_788_100_000,
};

const charityCopy = userEn.user.charity;

function withPickerChoices(
  element: ReactElement,
  availableChoices: readonly EndpointKeyChoice[] = choices,
): ReactElement {
  pickerHarness.choices = [...availableChoices];
  return element;
}

function createMutation(error: ApiError) {
  const mutation = {
    data: undefined,
    error: error as unknown,
    isPending: false,
    outcome: 'idle' as string,
    variables: undefined as unknown,
    refreshError: null as unknown,
    refresh: vi.fn(async () => undefined),
    reconcileGeneration: 0,
    reconcileError: null as unknown,
    isReconciling: false,
    retryReconcile: vi.fn(async () => undefined),
    reset: vi.fn(),
    mutateAsync: vi.fn(async (input: unknown) => {
      mutation.variables = structuredClone(input);
      mutation.outcome = error.status === 409 ? 'conflict' : 'unknown';
      throw error;
    }),
  };
  return mutation;
}

function successfulMutation(reconcileError: ApiError | null = null) {
  const mutation = {
    data: undefined,
    error: null,
    isPending: false,
    outcome: 'idle' as string,
    refreshError: reconcileError as unknown,
    refresh: vi.fn(async () => {
      mutation.outcome = 'confirmed';
      mutation.refreshError = null;
      mutation.reconcileGeneration += 1;
    }),
    reconcileGeneration: 0,
    reconcileError: reconcileError as unknown,
    isReconciling: false,
    retryReconcile: vi.fn(async () => {
      mutation.reconcileError = null;
      mutation.reconcileGeneration += 1;
    }),
    reset: vi.fn(),
    mutateAsync: vi.fn(async () => {
      mutation.outcome = reconcileError ? 'refresh-failed' : 'confirmed';
      if (reconcileError) {
        mutation.reconcileError = reconcileError;
      } else {
        mutation.reconcileGeneration += 1;
      }
      return undefined;
    }),
  };
  return mutation;
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

describe('public charity pricing', () => {
  it('keeps active filters reachable when a refresh leaves only one model', async () => {
    const extra = {
      ...CHARITY_OPEN.models[0],
      id: '8',
      fullName: '[公益]provider/extra',
      model: 'extra',
    };
    const rendered = await renderWithProviders(
      <CharityCapabilityPanel
        capability={{ ...CHARITY_OPEN, models: [...CHARITY_OPEN.models, extra] }}
      />,
      { station: 'user', role: 'user' },
    );
    await rendered.user.type(screen.getByRole('searchbox'), 'extra');
    rendered.rerender(<CharityCapabilityPanel capability={CHARITY_OPEN} />);
    expect(screen.getByRole('searchbox')).toHaveValue('extra');
    expect(screen.queryByRole('table')).toBeNull();
    await rendered.user.clear(screen.getByRole('searchbox'));
    expect(screen.getByRole('table', { name: charityCopy.priceCaption })).toBeVisible();
  });

  it('shows the server-projected current offer without donor economics', async () => {
    await renderWithProviders(<CharityCapabilityPanel capability={CHARITY_OPEN} />, {
      station: 'user',
      role: 'user',
    });
    const table = screen.getByRole('table', { name: charityCopy.priceCaption });
    expect(within(table).getByLabelText(`${charityCopy.originalPrice}: 3`)).toBeVisible();
    expect(within(table).getByLabelText(`${charityCopy.currentPrice}: 2.4`)).toBeVisible();
    expect(
      screen.getByText(charityCopy.discountPercent.replace('{{percent}}', '20')),
    ).toBeVisible();
    expect(screen.queryByText(charityCopy.donorReward)).toBeNull();
  });

  it('announces a scheduled token offer while showing base prices', async () => {
    const scheduled: CharityCapability = {
      ...CHARITY_OPEN,
      models: [
        {
          ...CHARITY_OPEN.models[0],
          effectiveDiscount: {
            source: 'fixed',
            percent: 100,
            next_change_at: CHARITY_OPEN.serverNow + 600,
          },
          pricing: {
            currentUserPricesMilli: {
              uncachedInput: '1000',
              cacheWriteInput: '2000',
              cacheReadInput: '500',
              output: '3000',
            },

            mode: 'per_token',
            userPricesMilli: {
              uncachedInput: '1000',
              cacheWriteInput: '2000',
              cacheReadInput: '500',
              output: '3000',
            },
            discountedUserPricesMilli: {
              uncachedInput: '500',
              cacheWriteInput: '1000',
              cacheReadInput: '250',
              output: '1500',
            },
          },
          discount: {
            enabled: true,
            percent: 50,
            startAt: CHARITY_OPEN.serverNow + 600,
            endAt: CHARITY_OPEN.serverNow + 1200,
          },
        },
      ],
    };
    await renderWithProviders(<CharityCapabilityPanel capability={scheduled} />, {
      station: 'user',
      role: 'user',
    });
    expect(screen.getByText(charityCopy.discountUpcoming, { exact: false })).toBeVisible();
    expect(screen.getByLabelText(`${charityCopy.currentPrice}: 0.5`)).toBeVisible();
    expect(screen.queryByLabelText(`${charityCopy.currentPrice}: 0.25`)).toBeNull();
  });
});

type TestUser = Awaited<ReturnType<typeof renderWithProviders>>['user'];

function donationCheckboxes() {
  return [
    ...screen.getAllByLabelText(/Select resource key/),
    screen.getByLabelText(charityCopy.ownershipAuthorization),
  ];
}

async function clickDonationCheckbox(user: TestUser, checkbox: HTMLElement) {
  const picker = checkbox.closest<HTMLElement>('.nb-expandable-panel');
  if (picker?.hidden) {
    await user.click(screen.getByRole('button', { name: charityCopy.presentation.openPicker }));
  }
  await user.click(checkbox);
}

async function declinePublicThanks(user: TestUser) {
  await user.click(
    within(screen.getByRole('radiogroup', { name: 'Accept a public Discord thank-you' })).getByRole(
      'radio',
      { name: 'No' },
    ),
  );
}

describe('donation composer recovery', () => {
  beforeEach(() => {
    sessionStorage.clear();
    pickerHarness.choices = [...choices];
    pickerHarness.readBlocked = false;
    pageMocks.useUserSession.mockReturnValue({
      data: { user: { id: '7', username: 'owner', effective_level: 1, donation_credit: '0' } },
      error: null,
      isPending: false,
      refetch: vi.fn(),
    });
    pageMocks.usePublicConfig.mockReturnValue({
      data: { announcementEpoch: 'announcement-1' },
    });
    vi.mocked(catalogModule.useCharityCatalog).mockReturnValue({
      data: {
        models: [],
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
        donationIntake: 'open',
        serverNow: 1_788_100_000,
      },
      error: null,
      isPending: false,
      isFetching: false,
      refetch: vi.fn(),
    } as never);
  });

  it('shows the configured donation notice and falls back to the built-in copy', async () => {
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(successfulMutation() as never);
    const rendered = await renderWithProviders(
      withPickerChoices(
        <DonationComposer
          draftNamespace="account-notice"
          notice={'Operator guidance\nCheck costs before sharing.'}
        />,
      ),
      { station: 'user', role: 'user' },
    );
    expect(screen.getByText(charityCopy.presentation.modelSetupHelp)).toBeVisible();
    expect(
      screen.getByRole('textbox', { name: charityCopy.donationDescription }),
    ).toHaveAccessibleDescription(charityCopy.presentation.descriptionHelp);
    await rendered.user.click(screen.getByText(charityCopy.disclosureTitle));
    expect(screen.getByRole('heading', { name: charityCopy.donationNoticeTitle })).toBeVisible();
    expect(screen.getByText(/Operator guidance/)).toHaveTextContent(
      'Operator guidance Check costs before sharing.',
    );

    rendered.rerender(
      withPickerChoices(<DonationComposer draftNamespace="account-notice" notice="" />),
    );
    expect(screen.getByText(charityCopy.donationNoticeDefault)).toBeVisible();
  });

  it('retains an unknown submission across announcement changes and retries the same intent', async () => {
    const mutation = createMutation(new ApiError('network_error', 'response lost', 0));
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const Host = ({ epoch }: { epoch: string }) => (
      <section data-epoch={epoch}>
        {withPickerChoices(<DonationComposer draftNamespace="account-recovery" />)}
      </section>
    );
    const view = await renderWithProviders(<Host epoch="first" />, {
      station: 'user',
      role: 'user',
    });
    await view.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'keep the original intent',
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(view.user, checkboxes[0]);
    await clickDonationCheckbox(view.user, checkboxes[1]);
    await declinePublicThanks(view.user);
    await view.user.click(screen.getByRole('button', { name: /submit for review/i }));
    const original = structuredClone(mutation.mutateAsync.mock.calls[0]![0]);
    view.rerender(<Host epoch="second" />);
    expect(screen.getByRole('textbox', { name: /donation description/i })).toHaveValue(
      'keep the original intent',
    );
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).toBeChecked();
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeDisabled();
    await view.user.click(
      screen.getByRole('button', { name: userEn.user.workflows.confirmDonation }),
    );
    expect(mutation.mutateAsync.mock.calls[1]![0]).toEqual(original);
  });

  it('preserves selected keys, authorization and description after an explicit conflict', async () => {
    const mutation = createMutation(new ApiError('conflict', 'revision changed', 409));
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const view = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="conflict" />),
      { station: 'user', role: 'user' },
    );
    await view.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'keep my draft',
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(view.user, checkboxes[0]);
    await clickDonationCheckbox(view.user, checkboxes[1]);
    await declinePublicThanks(view.user);
    await view.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(screen.getByRole('textbox', { name: /donation description/i })).toHaveValue(
      'keep my draft',
    );
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).toBeChecked();
    const remove = screen.getByRole('button', { name: /remove/i });
    expect(remove).toBeEnabled();
  });

  it('can remove and submit selected keys while unrelated picker reads fail', async () => {
    pickerHarness.readBlocked = true;
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const view = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="browse-failed" />),
      { station: 'user', role: 'user' },
    );
    await view.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'selected valid resource',
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(view.user, checkboxes[0]);
    await clickDonationCheckbox(view.user, checkboxes[1]);
    await declinePublicThanks(view.user);
    expect(screen.getByRole('button', { name: /remove/i })).toBeEnabled();
    const submit = screen.getByRole('button', { name: /submit for review/i });
    expect(submit).toBeEnabled();
    await view.user.click(submit);
    expect(mutation.mutateAsync).toHaveBeenCalledTimes(1);
  });

  it('keeps the page composer unwritable while hidden and restores its draft on return', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    vi.mocked(economyQueries.useCharityCapability).mockReturnValue({
      data: CHARITY_OPEN,
      error: null,
      isPending: false,
      isFetching: false,
      refetch: vi.fn(),
    } as never);
    const rendered = await renderWithProviders(withPickerChoices(<CharityPage />), {
      station: 'user',
      role: 'user',
    });
    const user = rendered.user;
    await user.click(screen.getByRole('tab', { name: 'Donate my keys' }));
    const description = screen.getByRole('textbox', { name: /donation description/i });
    await user.type(description, 'draft kept while hidden');
    const keyCheckbox = screen.getByLabelText('Select resource key 61');
    await clickDonationCheckbox(user, keyCheckbox);
    await clickDonationCheckbox(user, donationCheckboxes()[1]);
    await declinePublicThanks(user);
    const submit = screen.getByRole('button', { name: /submit for review/i });
    expect(submit).toBeEnabled();

    await user.click(screen.getByRole('tab', { name: 'Shared models' }));
    expect(description).toBeDisabled();
    expect(keyCheckbox).toBeDisabled();
    expect(submit).toBeDisabled();
    await user.click(submit);
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(sessionStorage.getItem('nonbiri:charity-donation-draft:v1:7')).toBe(
      JSON.stringify({ description: 'draft kept while hidden' }),
    );

    await user.click(screen.getByRole('tab', { name: 'Donate my keys' }));
    await waitFor(() => expect(description).toBeEnabled());
    expect(description).toHaveValue('draft kept while hidden');
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeEnabled();
  });

  it('keeps a successful submit locked when reconciliation fails and retries only the read', async () => {
    const refreshFailure = new ApiError('network_error', 'refresh failed', 0);
    const mutation = successfulMutation(refreshFailure);
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="account-8c" />),
      { station: 'user', role: 'user' },
    );
    const user = rendered.user;
    await user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'Shared resource',
    );
    const keyCheckbox = screen.getByLabelText('Select resource key 61');
    await clickDonationCheckbox(user, keyCheckbox);
    await clickDonationCheckbox(user, donationCheckboxes()[1]);
    await declinePublicThanks(user);
    const submit = screen.getByRole('button', { name: /submit for review/i });
    await user.click(submit);

    await waitFor(() => expect(mutation.mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutation.reconcileGeneration).toBe(0);
    expect(submit).toBeDisabled();
    expect(keyCheckbox).toBeDisabled();
    const retry = screen.getByRole('button', { name: 'Check again' });
    expect(retry).toBeVisible();

    await user.click(retry);
    expect(mutation.refresh).toHaveBeenCalledTimes(1);
    expect(mutation.mutateAsync).toHaveBeenCalledTimes(1);
    expect(mutation.reconcileGeneration).toBe(1);

    rendered.rerender(withPickerChoices(<DonationComposer draftNamespace="account-8c" />));
    expect(screen.getByText(charityCopy.submitted)).toBeInTheDocument();
    const refreshedKeyCheckbox = screen.getByLabelText('Select resource key 61');
    expect(refreshedKeyCheckbox).toBeEnabled();
    await clickDonationCheckbox(user, refreshedKeyCheckbox);
    await clickDonationCheckbox(user, donationCheckboxes()[1]);
    await declinePublicThanks(user);
    expect(screen.getByRole('button', { name: /submit for review/i })).toBeEnabled();
    expect(mutation.mutateAsync).toHaveBeenCalledTimes(1);
  });

  it('requires a nonblank description without losing selected keys or authorization', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="account-9" />),
      { station: 'user', role: 'user' },
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(rendered.user, checkboxes[0]);
    await clickDonationCheckbox(rendered.user, checkboxes[1]);
    await declinePublicThanks(rendered.user);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(/description is required/i);
    await rendered.user.type(screen.getByRole('textbox', { name: /donation description/i }), '   ');
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(checkboxes[0]).toBeChecked();
    expect(checkboxes[1]).toBeChecked();
    await rendered.user.clear(screen.getByRole('textbox', { name: /donation description/i }));
    await rendered.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'A shared endpoint',
    );
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith({
        description: 'A shared endpoint',
        keys: [
          {
            endpointKeyId: '61',
            nativeServices: ['chat_completions', 'responses'],
            credentialId: '81',
            credentialVersion: '1',
            grantRevision: '1',
            expiresAt: null,
            failureDisableThreshold: '10',
          },
        ],
        ownershipAuthorized: true,
        discordPublicThanks: false,
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
      withPickerChoices(<DonationComposer draftNamespace="account-10" />, [
        { ...choices[0], eligibility: 'already_donated' },
      ]),
      { station: 'user', role: 'user' },
    );
    expect(screen.getByRole('textbox', { name: /donation description/i })).toHaveValue(
      'return-safe draft',
    );
    rendered.rerender(withPickerChoices(<DonationComposer draftNamespace="account-10" />));
    expect(screen.getByRole('textbox', { name: /donation description/i })).toHaveValue(
      'return-safe draft',
    );
    expect(rendered.container.querySelector('input[type="password"]')).toBeNull();
  });

  it('keeps all authorized services and the exact catalog snapshot across mainstream key reselection', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const source = {
      id: '51',
      connection_id: '2',
      name: 'Catalog',
      base_url: 'https://catalog.example',
      models_url: 'https://catalog.example/models',
      list_format: 'openai',
      credential_id: '81',
      credential_version: '1',
      revision: '3',
      discovery_source_revision: '7',
      enabled: true,
      credential_available: true,
      state: 'succeeded',
      checked_at: 1,
      safe_class: 'none',
      safe_diag: '',
      created_at: 1,
      updated_at: 2,
    };
    installJsonFetchFixtures([{ method: 'GET', path: '/api/discovery-sources/51', body: source }]);
    const choice = {
      ...mainstreamChoice,
      key: { ...mainstreamChoice.key, discoverySourceId: '51' },
    };
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="mainstream-defaults" />, [choice]),
      { station: 'user', role: 'user' },
    );
    await rendered.user.click(
      screen.getByRole('button', { name: charityCopy.presentation.openPicker }),
    );
    await rendered.user.click(screen.getByLabelText('Select resource key 62'));
    expect(screen.queryByRole('checkbox', { name: 'Chat completions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Share this catalog' })).toBeNull();
    await rendered.user.click(screen.getByLabelText('Select resource key 62'));
    await rendered.user.click(screen.getByLabelText('Select resource key 62'));
    await rendered.queryClient.invalidateQueries({ queryKey: ['user'] });
    expect(screen.getByLabelText('Select resource key 62')).toBeChecked();
    await rendered.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'Share selected services',
    );
    await declinePublicThanks(rendered.user);
    await rendered.user.click(screen.getByLabelText(charityCopy.ownershipAuthorization));
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith({
        description: 'Share selected services',
        ownershipAuthorized: true,
        discordPublicThanks: false,
        keys: [
          {
            endpointKeyId: '62',
            nativeServices: ['chat_completions', 'responses'],
            credentialId: '81',
            credentialVersion: '1',
            grantRevision: '1',
            expiresAt: null,
            failureDisableThreshold: '10',
          },
        ],
      }),
    );
  });

  it('defaults mainstream donations without a catalog to available services only and custom donations to their authorized services', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="defaults-without-catalog" />, [
        mainstreamChoice,
        choices[0],
      ]),
      { station: 'user', role: 'user' },
    );
    await rendered.user.click(
      screen.getByRole('button', { name: charityCopy.presentation.openPicker }),
    );
    await rendered.user.click(screen.getByLabelText('Select resource key 62'));
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Share this catalog' })).toBeNull();
    await rendered.user.click(screen.getByLabelText('Select resource key 62'));
    await rendered.user.click(screen.getByLabelText('Select resource key 61'));
    expect(screen.getByLabelText('Select resource key 61')).toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'Chat completions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    await rendered.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'Share authorized services',
    );
    await declinePublicThanks(rendered.user);
    await rendered.user.click(screen.getByLabelText(charityCopy.ownershipAuthorization));
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          keys: [
            expect.objectContaining({
              endpointKeyId: '61',
              nativeServices: choices[0].key.nativeServices,
              credentialId: '81',
              credentialVersion: '1',
              grantRevision: '1',
            }),
          ],
        }),
      ),
    );
  });

  it('requires a pure mainstream channel or a custom-only selection', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="account-mainstream-mixed" />, [
        choices[0],
        mainstreamChoice,
      ]),
      { station: 'user', role: 'user' },
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(rendered.user, checkboxes[0]);
    await clickDonationCheckbox(rendered.user, checkboxes[1]);
    await declinePublicThanks(rendered.user);
    await clickDonationCheckbox(rendered.user, checkboxes[2]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('blocks a cross-channel mainstream selection before submission', async () => {
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="account-mainstream-cross-channel" />, [
        mainstreamChoice,
        otherMainstreamChoice,
      ]),
      { station: 'user', role: 'user' },
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(rendered.user, checkboxes[0]);
    await clickDonationCheckbox(rendered.user, checkboxes[1]);
    await declinePublicThanks(rendered.user);
    await clickDonationCheckbox(rendered.user, checkboxes[2]);
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('submits one local expiry per selected key using the server resolution', async () => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const local = '2030-01-01T00:00:00';
    const instant = Math.floor(new Date(2030, 0, 1).getTime() / 1000);
    installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/time-zones',
        body: { version: 'go1.26.6-zoneinfo', zones: [...new Set([zone, 'UTC'])].sort() },
      },
      {
        method: 'GET',
        path: `/api/time/resolve?${new URLSearchParams({ local, time_zone: zone })}`,
        body: {
          instant,
          local,
          time_zone: zone,
          offset_seconds: Date.parse(`${local}Z`) / 1000 - instant,
          adjustment: 'none',
        },
      },
    ]);
    const mutation = successfulMutation();
    vi.mocked(economyQueries.useCreateDonation).mockReturnValue(mutation as never);
    const rendered = await renderWithProviders(
      withPickerChoices(<DonationComposer draftNamespace="account-key-expiry" />, [choices[0]]),
      { station: 'user', role: 'user' },
    );
    await rendered.user.type(
      screen.getByRole('textbox', { name: /donation description/i }),
      'Shared resource',
    );
    const checkboxes = donationCheckboxes();
    await clickDonationCheckbox(rendered.user, checkboxes[0]);
    const expiry = rendered.container.querySelector('input[type="datetime-local"]');
    if (!expiry) throw new Error('expiry control not rendered');
    await rendered.user.type(expiry, '2030-01-01T00:00');
    expect(checkboxes[0]).toBeChecked();
    await clickDonationCheckbox(rendered.user, checkboxes[1]);
    await declinePublicThanks(rendered.user);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /submit for review/i })).toBeEnabled(),
    );
    await rendered.user.click(screen.getByRole('button', { name: /submit for review/i }));
    await waitFor(() =>
      expect(mutation.mutateAsync).toHaveBeenCalledWith({
        description: 'Shared resource',
        keys: [
          {
            endpointKeyId: '61',
            nativeServices: ['chat_completions', 'responses'],
            credentialId: '81',
            credentialVersion: '1',
            grantRevision: '1',
            expiresAt: instant,
            failureDisableThreshold: '10',
          },
        ],
        ownershipAuthorized: true,
        discordPublicThanks: false,
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
      rateLimitedUntil: null,
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
      failureDisableThreshold: '10',
      smartBackoff: null,
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
    vi.mocked(economyQueries.useEditDonation).mockReturnValue(successfulMutation() as never);
    vi.mocked(economyQueries.useWithdrawDonation).mockReturnValue(successfulMutation() as never);
    vi.mocked(economyQueries.useTerminateDonation).mockReturnValue(successfulMutation() as never);
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

  it('keeps physical, failure, quota and temporary rate-limit states visibly separate', async () => {
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
      rateLimitedUntil: 1_900_000_000,
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
      failureDisableThreshold: '10',
      smartBackoff: null,
      streak: { generation: '1', count: '10', failureDisabled: true },
      endedReason: null,
    };
    const rendered = await renderWithProviders(<DonationKeyPanel donationKey={key} />, {
      station: 'user',
      role: 'user',
    });
    expect(screen.getByText(charityCopy.keyState.physical_disabled)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.failure_disabled)).toBeInTheDocument();
    expect(screen.getByText('Upstream wait')).toBeInTheDocument();
    rendered.rerender(
      <DonationKeyPanel
        donationKey={{
          ...key,
          physicalEnabled: true,
          charityState: 'exhausted',
          limits: { price: '0', calls: '0', tokens: '0' },
          failureDisableThreshold: '10',
          smartBackoff: null,
          streak: { generation: '1', count: '0', failureDisabled: false },
        }}
      />,
    );
    expect(screen.getByText(charityCopy.keyState.price_exhausted)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.calls_exhausted)).toBeInTheDocument();
    expect(screen.getByText(charityCopy.keyState.tokens_exhausted)).toBeInTheDocument();
    expect(screen.getByText('Upstream wait')).toBeInTheDocument();
    rendered.rerender(
      <DonationKeyPanel
        donationKey={{
          ...key,
          physicalEnabled: true,
          charityState: 'rate_limited',
          rateLimitedUntil: 1_900_000_000,
          streak: { generation: '1', count: '0', failureDisabled: false },
        }}
      />,
    );
    expect(screen.getAllByText(charityCopy.keyState.rate_limited).length).toBeGreaterThan(0);
    expect(screen.getByText(/Expected recovery:/)).toBeInTheDocument();
    expect(screen.queryByText(charityCopy.keyState.physical_disabled)).not.toBeInTheDocument();
    expect(screen.queryByText(charityCopy.keyState.disabled)).not.toBeInTheDocument();
    rendered.rerender(<DonationKeyPanel donationKey={{ ...key, rateLimitedUntil: 1 }} />);
    expect(screen.queryByRole('region', { name: 'Recovery status' })).not.toBeInTheDocument();
    expect(screen.queryByText('Recovery status')).not.toBeInTheDocument();
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
      if (!showDetailLink) {
        expect(screen.getByText(charityCopy.presentation.modelSetupHelp)).toBeVisible();
      }
      await rendered.user.click(screen.getByRole('button', { name: /edit/i }));
      const description = screen.getByRole('textbox', { name: /donation description/i });
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
      expect(screen.getByRole('textbox', { name: /donation description/i })).toHaveValue(
        'preserved safe edit',
      );
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
