import { describe, expect, it } from 'vitest';
import {
  bindingDraftReducer,
  callerKeyMachineReducer,
  endpointSecretDraftReducer,
  initialBindingDraftState,
  initialCallerKeyMachineState,
  initialEndpointSecretDraftState,
  initialLifecycleMachineState,
  initialResourceMutationState,
  lifecycleMachineReducer,
  resourceMutationReducer,
} from './stateMachines';
import type { BindingCandidate, CallerKeyAuthority } from './types';

const authority = (generation: string): CallerKeyAuthority => ({
  generation,
  metadata: {
    display: 'nbk_AAAA…AAAA',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_000,
    generation,
  },
});

const candidate = (keyId: string, model: string): BindingCandidate => ({
  endpoint_key_id: keyId,
  endpoint_base_url: 'https://example.com/v1',
  connector_type: 'openai-compatible',
  endpoint_note: '',
  endpoint_key_display_head: 'head',
  endpoint_key_display_tail: 'tail',
  endpoint_key_note: '',
  upstream_model_id: model,
  source_types: ['manual'],
});

describe('core state machines', () => {
  it('keeps an endpoint secret after validation/request errors and clears it at every boundary', () => {
    let state = initialEndpointSecretDraftState('account-a', 'page-a');
    state = endpointSecretDraftReducer(state, {
      type: 'change',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      secret: 'synthetic-secret',
    });
    state = endpointSecretDraftReducer(state, {
      type: 'request-error',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      message: 'failed',
    });
    expect(state.secret).toBe('synthetic-secret');

    const stale = endpointSecretDraftReducer(state, {
      type: 'success',
      accountId: 'account-b',
      pageInstanceId: 'page-a',
    });
    expect(stale).toBe(state);
    expect(
      endpointSecretDraftReducer(state, {
        type: 'cancel',
        accountId: 'account-a',
        pageInstanceId: 'page-a',
      }).secret,
    ).toBe('');
    expect(
      endpointSecretDraftReducer(state, {
        type: 'boundary',
        accountId: 'account-b',
        pageInstanceId: 'page-b',
      }),
    ).toEqual(initialEndpointSecretDraftState('account-b', 'page-b'));
  });

  it('matches CallerKey plaintext to account, page, action, and generation', () => {
    let state = initialCallerKeyMachineState('account-a', 'page-a');
    state = callerKeyMachineReducer(state, {
      type: 'read-success',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      authority: authority('0'),
    });
    state = callerKeyMachineReducer(state, {
      type: 'regenerate-start',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      actionId: 'action-a',
      expectedGeneration: '0',
    });
    const stale = callerKeyMachineReducer(state, {
      type: 'regenerate-success',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      actionId: 'action-b',
      expectedGeneration: '0',
      secret: 'synthetic-secret',
      metadata: authority('1').metadata!,
    });
    expect(stale).toBe(state);

    state = callerKeyMachineReducer(state, {
      type: 'regenerate-success',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      actionId: 'action-a',
      expectedGeneration: '0',
      secret: 'synthetic-secret',
      metadata: authority('1').metadata!,
    });
    expect(state.reveal?.secret).toBe('synthetic-secret');
    state = callerKeyMachineReducer(state, {
      type: 'read-error',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      message: 'refresh failed',
    });
    expect(state.reveal?.secret).toBe('synthetic-secret');
    state = callerKeyMachineReducer(state, {
      type: 'read-success',
      accountId: 'account-a',
      pageInstanceId: 'page-a',
      authority: authority('2'),
    });
    expect(state.reveal).toBeNull();
    expect(
      callerKeyMachineReducer(state, {
        type: 'boundary',
        accountId: 'account-b',
        pageInstanceId: 'page-b',
      }).accountId,
    ).toBe('account-b');
  });

  it('keeps binding selections exact by key/model pair and ignores late action results', () => {
    let state = initialBindingDraftState('account-a', 'model-a', '0');
    state = bindingDraftReducer(state, {
      type: 'toggle',
      accountId: 'account-a',
      modelId: 'model-a',
      candidate: candidate('21', 'Exact'),
    });
    state = bindingDraftReducer(state, {
      type: 'toggle',
      accountId: 'account-a',
      modelId: 'model-a',
      candidate: candidate('22', 'Exact'),
    });
    expect(state.selections).toHaveLength(2);
    state = bindingDraftReducer(state, {
      type: 'submit',
      accountId: 'account-a',
      modelId: 'model-a',
      actionId: 'action-a',
    });
    const stale = bindingDraftReducer(state, {
      type: 'result',
      accountId: 'account-a',
      modelId: 'model-a',
      actionId: 'action-b',
      outcome: 'success',
      bindingRevision: '1',
    });
    expect(stale).toBe(state);
    state = bindingDraftReducer(state, {
      type: 'result',
      accountId: 'account-a',
      modelId: 'model-a',
      actionId: 'action-a',
      outcome: 'unknown',
      bindingRevision: '0',
    });
    state = bindingDraftReducer(state, {
      type: 'authoritative',
      accountId: 'account-a',
      modelId: 'model-a',
      bindingRevision: '1',
    });
    expect(state).toMatchObject({ status: 'idle', bindingRevision: '1' });
    expect(
      bindingDraftReducer(state, {
        type: 'boundary',
        accountId: 'account-b',
        modelId: 'model-a',
        bindingRevision: '0',
      }).selections,
    ).toEqual([]);
  });

  it('rejects stale resource and lifecycle completions after an account/action boundary', () => {
    let mutation = initialResourceMutationState('account-a');
    mutation = resourceMutationReducer(mutation, {
      type: 'start',
      accountId: 'account-a',
      actionId: 'action-a',
    });
    expect(
      resourceMutationReducer(mutation, {
        type: 'success',
        accountId: 'account-a',
        actionId: 'action-b',
      }),
    ).toBe(mutation);
    expect(
      resourceMutationReducer(mutation, { type: 'account', accountId: 'account-b' }).outcome,
    ).toBe('idle');

    let lifecycle = initialLifecycleMachineState('account-a');
    lifecycle = lifecycleMachineReducer(lifecycle, {
      type: 'confirm',
      accountId: 'account-a',
      intent: 'delete',
    });
    lifecycle = lifecycleMachineReducer(lifecycle, {
      type: 'start',
      accountId: 'account-a',
      intent: 'delete',
      actionId: 'action-a',
    });
    const stale = lifecycleMachineReducer(lifecycle, {
      type: 'complete',
      accountId: 'account-a',
      intent: 'delete',
      actionId: 'action-b',
    });
    expect(stale).toBe(lifecycle);
    expect(
      lifecycleMachineReducer(lifecycle, { type: 'boundary', accountId: 'account-b' }),
    ).toEqual(initialLifecycleMachineState('account-b'));
  });

  it('keeps unknown lifecycle outcomes non-replayable and only checks deletion authority', () => {
    let deletion = lifecycleMachineReducer(initialLifecycleMachineState('account-a'), {
      type: 'confirm',
      accountId: 'account-a',
      intent: 'delete',
    });
    deletion = lifecycleMachineReducer(deletion, {
      type: 'start',
      accountId: 'account-a',
      intent: 'delete',
      actionId: 'local-action',
    });
    deletion = lifecycleMachineReducer(deletion, {
      type: 'uncertain',
      accountId: 'account-a',
      intent: 'delete',
      actionId: 'local-action',
      message: 'check authority',
    });
    expect(deletion).toMatchObject({
      status: 'unknown',
      actionId: null,
      message: 'check authority',
    });

    const checking = lifecycleMachineReducer(deletion, {
      type: 'check-start',
      accountId: 'account-a',
    });
    expect(checking.status).toBe('checking');
    expect(
      lifecycleMachineReducer(checking, {
        type: 'authority-active',
        accountId: 'account-a',
        message: 'still active',
      }),
    ).toMatchObject({ status: 'active', message: 'still active' });

    let exportState = lifecycleMachineReducer(initialLifecycleMachineState('account-a'), {
      type: 'confirm',
      accountId: 'account-a',
      intent: 'export',
    });
    exportState = lifecycleMachineReducer(exportState, {
      type: 'start',
      accountId: 'account-a',
      intent: 'export',
      actionId: 'local-export',
    });
    exportState = lifecycleMachineReducer(exportState, {
      type: 'uncertain',
      accountId: 'account-a',
      intent: 'export',
      actionId: 'local-export',
      message: 'authorize again',
    });
    expect(
      lifecycleMachineReducer(exportState, { type: 'check-start', accountId: 'account-a' }),
    ).toBe(exportState);
  });
});
