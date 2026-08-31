import type {
  BindingCandidate,
  BindingSelection,
  CallerKeyAuthority,
  CallerKeyMetadata,
  LifecycleIntent,
} from './types';

export type MutationOutcome = 'idle' | 'pending' | 'success' | 'conflict' | 'unknown' | 'error';

export interface ResourceMutationState {
  accountId: string;
  outcome: MutationOutcome;
  actionId: string | null;
  message: string | null;
}

export type ResourceMutationEvent =
  | { type: 'account'; accountId: string }
  | { type: 'start'; accountId: string; actionId: string }
  | { type: 'success'; accountId: string; actionId: string }
  | { type: 'conflict'; accountId: string; actionId: string }
  | { type: 'unknown'; accountId: string; actionId: string }
  | { type: 'error'; accountId: string; actionId: string; message: string }
  | { type: 'reset'; accountId: string };

export function initialResourceMutationState(accountId: string): ResourceMutationState {
  return { accountId, outcome: 'idle', actionId: null, message: null };
}

export function resourceMutationReducer(
  state: ResourceMutationState,
  event: ResourceMutationEvent,
): ResourceMutationState {
  if (event.type === 'account' || event.type === 'reset') {
    return initialResourceMutationState(event.accountId);
  }
  if (event.accountId !== state.accountId) return state;
  if (event.type === 'start') {
    if (state.outcome === 'pending') return state;
    return {
      accountId: state.accountId,
      outcome: 'pending',
      actionId: event.actionId,
      message: null,
    };
  }
  if (state.outcome !== 'pending' || state.actionId !== event.actionId) return state;
  if (event.type === 'success') return { ...state, outcome: 'success', message: null };
  if (event.type === 'conflict') return { ...state, outcome: 'conflict', message: null };
  if (event.type === 'unknown') return { ...state, outcome: 'unknown', message: null };
  return { ...state, outcome: 'error', message: event.message };
}

export interface EndpointSecretDraftState {
  accountId: string;
  pageInstanceId: string;
  secret: string;
  ownershipConfirmed: boolean;
  status: 'editing' | 'submitting' | 'error' | 'cleared';
  message: string | null;
}

export type EndpointSecretDraftEvent =
  | { type: 'change'; accountId: string; pageInstanceId: string; secret: string }
  | { type: 'ownership'; accountId: string; pageInstanceId: string; confirmed: boolean }
  | { type: 'submit'; accountId: string; pageInstanceId: string }
  | { type: 'local-error'; accountId: string; pageInstanceId: string; message: string }
  | { type: 'request-error'; accountId: string; pageInstanceId: string; message: string }
  | { type: 'success' | 'cancel' | 'leave'; accountId: string; pageInstanceId: string }
  | { type: 'boundary'; accountId: string; pageInstanceId: string };

export function initialEndpointSecretDraftState(
  accountId: string,
  pageInstanceId: string,
): EndpointSecretDraftState {
  return {
    accountId,
    pageInstanceId,
    secret: '',
    ownershipConfirmed: false,
    status: 'editing',
    message: null,
  };
}

export function endpointSecretDraftReducer(
  state: EndpointSecretDraftState,
  event: EndpointSecretDraftEvent,
): EndpointSecretDraftState {
  if (event.type === 'boundary')
    return initialEndpointSecretDraftState(event.accountId, event.pageInstanceId);
  if (event.accountId !== state.accountId || event.pageInstanceId !== state.pageInstanceId)
    return state;
  if (event.type === 'change')
    return { ...state, secret: event.secret, status: 'editing', message: null };
  if (event.type === 'ownership')
    return { ...state, ownershipConfirmed: event.confirmed, status: 'editing', message: null };
  if (event.type === 'submit')
    return state.status === 'submitting'
      ? state
      : { ...state, status: 'submitting', message: null };
  if (event.type === 'local-error' || event.type === 'request-error') {
    return { ...state, status: 'error', message: event.message };
  }
  return {
    ...state,
    secret: '',
    ownershipConfirmed: false,
    status: 'cleared',
    message: null,
  };
}

export interface CallerKeyReveal {
  secret: string;
  actionId: string;
  generation: string;
}

export interface CallerKeyMachineState {
  accountId: string;
  pageInstanceId: string;
  authority: CallerKeyAuthority | null;
  readState: 'loading' | 'ready' | 'error';
  mutation: MutationOutcome;
  activeAction: { actionId: string; expectedGeneration: string } | null;
  reveal: CallerKeyReveal | null;
  readError: string | null;
  mutationError: string | null;
}

export type CallerKeyMachineEvent =
  | { type: 'boundary'; accountId: string; pageInstanceId: string }
  | { type: 'read-start'; accountId: string; pageInstanceId: string }
  | {
      type: 'read-success';
      accountId: string;
      pageInstanceId: string;
      authority: CallerKeyAuthority;
    }
  | { type: 'read-error'; accountId: string; pageInstanceId: string; message: string }
  | {
      type: 'regenerate-start';
      accountId: string;
      pageInstanceId: string;
      actionId: string;
      expectedGeneration: string;
    }
  | {
      type: 'regenerate-success';
      accountId: string;
      pageInstanceId: string;
      actionId: string;
      expectedGeneration: string;
      secret: string;
      metadata: CallerKeyMetadata;
    }
  | {
      type: 'regenerate-failure';
      accountId: string;
      pageInstanceId: string;
      actionId: string;
      expectedGeneration: string;
      outcome: 'conflict' | 'unknown' | 'error';
      message?: string;
    }
  | { type: 'close-reveal'; accountId: string; pageInstanceId: string };

export function initialCallerKeyMachineState(
  accountId: string,
  pageInstanceId: string,
): CallerKeyMachineState {
  return {
    accountId,
    pageInstanceId,
    authority: null,
    readState: 'loading',
    mutation: 'idle',
    activeAction: null,
    reveal: null,
    readError: null,
    mutationError: null,
  };
}

function callerBoundaryMatches(
  state: CallerKeyMachineState,
  event: { accountId: string; pageInstanceId: string },
): boolean {
  return event.accountId === state.accountId && event.pageInstanceId === state.pageInstanceId;
}

export function callerKeyMachineReducer(
  state: CallerKeyMachineState,
  event: CallerKeyMachineEvent,
): CallerKeyMachineState {
  if (event.type === 'boundary')
    return initialCallerKeyMachineState(event.accountId, event.pageInstanceId);
  if (!callerBoundaryMatches(state, event)) return state;
  if (event.type === 'read-start') return { ...state, readState: 'loading', readError: null };
  if (event.type === 'read-error') {
    return { ...state, readState: 'error', readError: event.message };
  }
  if (event.type === 'read-success') {
    const reveal =
      state.reveal && state.reveal.generation === event.authority.generation ? state.reveal : null;
    return {
      ...state,
      authority: event.authority,
      readState: 'ready',
      readError: null,
      reveal,
    };
  }
  if (event.type === 'close-reveal') {
    return { ...state, reveal: null, mutation: 'idle', mutationError: null, activeAction: null };
  }
  if (event.type === 'regenerate-start') {
    if (
      state.mutation === 'pending' ||
      state.reveal ||
      state.authority?.generation !== event.expectedGeneration
    ) {
      return state;
    }
    return {
      ...state,
      mutation: 'pending',
      activeAction: { actionId: event.actionId, expectedGeneration: event.expectedGeneration },
      mutationError: null,
    };
  }
  const active = state.activeAction;
  if (
    state.mutation !== 'pending' ||
    !active ||
    active.actionId !== event.actionId ||
    active.expectedGeneration !== event.expectedGeneration
  ) {
    return state;
  }
  if (event.type === 'regenerate-success') {
    return {
      ...state,
      authority: { generation: event.metadata.generation, metadata: event.metadata },
      readState: 'ready',
      mutation: 'success',
      activeAction: null,
      reveal: {
        secret: event.secret,
        actionId: event.actionId,
        generation: event.metadata.generation,
      },
      mutationError: null,
    };
  }
  return {
    ...state,
    mutation: event.outcome,
    activeAction: null,
    mutationError: event.message ?? null,
  };
}

export interface BindingDraftState {
  accountId: string;
  modelId: string;
  bindingRevision: string;
  selections: BindingSelection[];
  status: MutationOutcome;
  actionId: string | null;
}

export type BindingDraftEvent =
  | { type: 'boundary'; accountId: string; modelId: string; bindingRevision: string }
  | { type: 'authoritative'; accountId: string; modelId: string; bindingRevision: string }
  | { type: 'toggle'; accountId: string; modelId: string; candidate: BindingCandidate }
  | { type: 'candidate-invalid'; accountId: string; modelId: string; candidate: BindingSelection }
  | { type: 'submit'; accountId: string; modelId: string; actionId: string }
  | {
      type: 'result';
      accountId: string;
      modelId: string;
      actionId: string;
      outcome: Exclude<MutationOutcome, 'idle' | 'pending'>;
      bindingRevision?: string;
    };

export function initialBindingDraftState(
  accountId: string,
  modelId: string,
  bindingRevision: string,
): BindingDraftState {
  return { accountId, modelId, bindingRevision, selections: [], status: 'idle', actionId: null };
}

function selectionKey(value: BindingSelection): string {
  return `${value.endpoint_key_id}\u0000${value.upstream_model_id}`;
}

export function bindingDraftReducer(
  state: BindingDraftState,
  event: BindingDraftEvent,
): BindingDraftState {
  if (event.type === 'boundary') {
    return initialBindingDraftState(event.accountId, event.modelId, event.bindingRevision);
  }
  if (event.accountId !== state.accountId || event.modelId !== state.modelId) return state;
  if (event.type === 'authoritative') {
    if (state.status === 'pending') return state;
    return { ...state, bindingRevision: event.bindingRevision, status: 'idle', actionId: null };
  }
  if (event.type === 'toggle') {
    if (state.status === 'pending') return state;
    const selection = {
      endpoint_key_id: event.candidate.endpoint_key_id,
      upstream_model_id: event.candidate.upstream_model_id,
    };
    const key = selectionKey(selection);
    const exists = state.selections.some((candidate) => selectionKey(candidate) === key);
    return {
      ...state,
      status: 'idle',
      selections: exists
        ? state.selections.filter((candidate) => selectionKey(candidate) !== key)
        : [...state.selections, selection],
    };
  }
  if (event.type === 'candidate-invalid') {
    const key = selectionKey(event.candidate);
    return {
      ...state,
      selections: state.selections.filter((candidate) => selectionKey(candidate) !== key),
    };
  }
  if (event.type === 'submit') {
    if (state.status === 'pending' || state.selections.length === 0) return state;
    return { ...state, status: 'pending', actionId: event.actionId };
  }
  if (state.status !== 'pending' || state.actionId !== event.actionId) return state;
  return {
    ...state,
    status: event.outcome,
    actionId: null,
    bindingRevision: event.bindingRevision ?? state.bindingRevision,
    selections: event.outcome === 'success' ? [] : state.selections,
  };
}

export interface LifecycleMachineState {
  accountId: string;
  intent: LifecycleIntent | null;
  status:
    | 'idle'
    | 'elevating'
    | 'confirming'
    | 'pending'
    | 'unknown'
    | 'checking'
    | 'active'
    | 'error'
    | 'complete';
  actionId: string | null;
  message: string | null;
}

export type LifecycleMachineEvent =
  | { type: 'boundary'; accountId: string }
  | { type: 'elevate'; accountId: string; intent: LifecycleIntent }
  | { type: 'elevation-error'; accountId: string; intent: LifecycleIntent; message: string }
  | { type: 'confirm'; accountId: string; intent: LifecycleIntent }
  | { type: 'start'; accountId: string; intent: LifecycleIntent; actionId: string }
  | {
      type: 'uncertain';
      accountId: string;
      intent: LifecycleIntent;
      actionId: string;
      message: string;
    }
  | { type: 'check-start'; accountId: string }
  | { type: 'authority-active'; accountId: string; message: string }
  | { type: 'authority-error'; accountId: string; message: string }
  | { type: 'error'; accountId: string; intent: LifecycleIntent; actionId: string; message: string }
  | { type: 'complete'; accountId: string; intent: LifecycleIntent; actionId?: string }
  | { type: 'cancel'; accountId: string };

export function initialLifecycleMachineState(accountId: string): LifecycleMachineState {
  return {
    accountId,
    intent: null,
    status: 'idle',
    actionId: null,
    message: null,
  };
}

export function lifecycleMachineReducer(
  state: LifecycleMachineState,
  event: LifecycleMachineEvent,
): LifecycleMachineState {
  if (event.type === 'boundary') return initialLifecycleMachineState(event.accountId);
  if (event.accountId !== state.accountId) return state;
  if (event.type === 'cancel') return initialLifecycleMachineState(state.accountId);
  if (event.type === 'elevate') {
    return {
      ...state,
      intent: event.intent,
      status: 'elevating',
      actionId: null,
      message: null,
    };
  }
  if (event.type === 'elevation-error') {
    if (state.status !== 'elevating' || state.intent !== event.intent) return state;
    return { ...state, status: 'error', message: event.message };
  }
  if (event.type === 'confirm') {
    return {
      ...state,
      intent: event.intent,
      status: 'confirming',
      actionId: null,
      message: null,
    };
  }
  if (event.type === 'start') {
    if (state.status !== 'confirming' || state.intent !== event.intent) return state;
    return { ...state, status: 'pending', actionId: event.actionId, message: null };
  }
  if (event.type === 'complete' && event.actionId === undefined && state.status === 'elevating') {
    return { ...state, status: 'complete' };
  }
  if (event.type === 'check-start') {
    return (state.status === 'unknown' || state.status === 'active') && state.intent === 'delete'
      ? { ...state, status: 'checking', message: null }
      : state;
  }
  if (event.type === 'authority-active') {
    return state.status === 'checking' && state.intent === 'delete'
      ? { ...state, status: 'active', message: event.message }
      : state;
  }
  if (event.type === 'authority-error') {
    return state.status === 'checking' && state.intent === 'delete'
      ? { ...state, status: 'unknown', message: event.message }
      : state;
  }
  if (
    state.status !== 'pending' ||
    state.intent !== event.intent ||
    state.actionId !== event.actionId
  ) {
    return state;
  }
  if (event.type === 'uncertain') {
    return { ...state, status: 'unknown', actionId: null, message: event.message };
  }
  if (event.type === 'error') {
    return { ...state, status: 'error', message: event.message, actionId: null };
  }
  return { ...state, status: 'complete', actionId: null, message: null };
}
