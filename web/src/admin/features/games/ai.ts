import { gameRequest } from '../../../user/games/common/request';
import type { AIAdminState, AIPolicyDefinition, AIPreview } from '@shared/aiPlayers';
const root = '/admin/api/games/bidding/ai';
export async function readAI(signal: AbortSignal) {
  return (await gameRequest<AIAdminState>(root, { signal, expectedStatuses: [200] })).data!;
}
export async function saveAI(
  input: { kind: 'settings' | 'policies' | 'bots'; body: unknown },
  key: string,
  signal: AbortSignal,
) {
  return (
    await gameRequest<unknown>(root + '/' + input.kind, {
      method: 'POST',
      json: input.body,
      idempotencyKey: key,
      signal,
      expectedStatuses: [200],
    })
  ).data;
}
export async function previewAI(
  definition: AIPolicyDefinition,
  scenario: string,
  signal?: AbortSignal,
) {
  return (
    await gameRequest<AIPreview>(root + '/preview', {
      method: 'POST',
      json: { definition, scenario },
      signal,
      expectedStatuses: [200],
    })
  ).data!;
}
