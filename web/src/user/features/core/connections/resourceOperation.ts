import { ApiError } from '@shared/query/http';
import { record, string } from '@shared/operations/wire';
import {
  executeResource,
  readResourceResult,
  type ResourceIntent,
  type ResourceResult,
  type ResourceStatus,
} from '../resourceOperation';
import { connectionAPI, connectionWrite, normalizeDiscoverySource, resourcePath } from './api';
import { resolveSuggestedDiscovery } from './suggestedDiscovery';
import type { Connection, Credential, DiscoverySource, DiscoverySourceInput } from './types';

export type ConnectionIntent =
  | ResourceIntent
  | { kind: 'connection'; input: { name: string; note: string } }
  | { kind: 'credential'; connectionId: string; input: { note: string; ownership_confirmed: true } }
  | { kind: 'credential_version'; credentialId: string; expectedRevision: string }
  | {
      kind: 'discovery_source';
      connectionId: string;
      input: DiscoverySourceInput;
      suggestedCredentialVersion?: string;
    }
  | { kind: 'source_refresh'; sourceId: string; expectedRevision: string };
export type ConnectionResult =
  | ResourceResult
  | { kind: 'connection'; connection: Connection }
  | { kind: 'credential' | 'credential_version'; credential: Credential }
  | { kind: 'discovery_source' | 'source_refresh'; source: DiscoverySource };

export async function executeConnection(
  intent: ConnectionIntent,
  secret: string | undefined,
  key: string,
  signal: AbortSignal,
): Promise<ConnectionResult> {
  switch (intent.kind) {
    case 'connection':
      return {
        kind: intent.kind,
        connection: await connectionAPI.create(intent.input, key, signal),
      };
    case 'credential':
      if (!secret) throw new ApiError('secret_required', 'Enter the credential again.', 400);
      return {
        kind: intent.kind,
        credential: await connectionAPI.createCredential(
          intent.connectionId,
          { ...intent.input, secret },
          key,
          signal,
        ),
      };
    case 'credential_version': {
      if (!secret) throw new ApiError('secret_required', 'Enter the credential again.', 400);
      await connectionWrite(
        '/api/credentials/' + resourcePath(intent.credentialId) + '/versions',
        'POST',
        { secret, ownership_confirmed: true, expected_revision: intent.expectedRevision },
        key,
        () => undefined,
        signal,
      );
      return {
        kind: intent.kind,
        credential: await connectionAPI.credential(intent.credentialId, signal),
      };
    }
    case 'discovery_source':
      return {
        kind: intent.kind,
        source: intent.suggestedCredentialVersion
          ? await resolveSuggestedDiscovery(
              intent.connectionId,
              intent.input,
              intent.suggestedCredentialVersion,
              key,
              signal,
            )
          : await connectionAPI.createSource(intent.connectionId, intent.input, key, signal),
      };
    case 'source_refresh': {
      const source = await connectionWrite(
        '/api/discovery-sources/' + resourcePath(intent.sourceId) + '/refresh',
        'POST',
        { expected_revision: intent.expectedRevision },
        key,
        (value) => {
          const result = record(value, ['operation_id', 'discovery_source'], 'source refresh');
          string(result.operation_id, 'refresh operation', { min: 1 });
          return normalizeDiscoverySource(result.discovery_source);
        },
        signal,
      );
      return { kind: intent.kind, source };
    }
    default:
      return executeResource(intent, secret, key, signal);
  }
}

export async function readConnectionResult(
  intent: ConnectionIntent,
  status: ResourceStatus,
  signal: AbortSignal,
): Promise<ConnectionResult | null> {
  if (!('stage' in status)) return null;
  if (
    ![
      'connection',
      'credential',
      'credential_version',
      'discovery_source',
      'source_refresh',
    ].includes(intent.kind)
  ) {
    return readResourceResult(intent as ResourceIntent, status, signal);
  }
  if (status.stage !== intent.kind)
    throw new ApiError('invalid_response', 'The saved operation does not match this action.', 200);
  const resultID = (name: string) => {
    const value = status.result[name];
    if (typeof value !== 'string')
      throw new ApiError('invalid_response', 'The saved operation has no resource identity.', 200);
    return value;
  };
  switch (intent.kind) {
    case 'connection':
      return {
        kind: intent.kind,
        connection: await connectionAPI.get(resultID('connection_id'), signal),
      };
    case 'credential':
    case 'credential_version': {
      const credentialId = resultID('credential_id');
      if (intent.kind === 'credential_version' && credentialId !== intent.credentialId)
        throw new ApiError('invalid_response', 'The saved credential changed.', 200);
      return {
        kind: intent.kind,
        credential: await connectionAPI.credential(credentialId, signal),
      };
    }
    case 'discovery_source':
    case 'source_refresh': {
      const sourceId = resultID('discovery_source_id');
      if (intent.kind === 'source_refresh' && sourceId !== intent.sourceId)
        throw new ApiError('invalid_response', 'The saved source changed.', 200);
      return { kind: intent.kind, source: await connectionAPI.source(sourceId, signal) };
    }
    default:
      return null;
  }
}
