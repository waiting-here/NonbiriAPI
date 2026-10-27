import type { Binding, BindingCandidate } from '../types';

export function candidateIdentity(
  candidate: Pick<BindingCandidate, 'endpoint_key_id' | 'upstream_model_id'>,
): string {
  return `${candidate.endpoint_key_id}\u0000${candidate.upstream_model_id}`;
}

export function boundIdentities(bindings: readonly Binding[]): Set<string> {
  return new Set(
    bindings.flatMap((binding) =>
      binding.targets?.length
        ? binding.targets.map(candidateIdentity)
        : [candidateIdentity(binding)],
    ),
  );
}
