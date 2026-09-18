import { enumValue, exactRecord } from '../common/strict';
import { amount, unique } from './value';
import type { LikesEvent } from './types';
import type { Seat } from '../common/duel/types';

export interface ResourceShortage {
  resource: 'energy' | 'burst' | 'sub' | 'api';
  required: number;
  available: number;
}
export interface Shortage {
  payment: 'energy' | 'mix' | 'api' | 'sub';
  resources: ResourceShortage[];
}
export function shortageValue(value: unknown): Shortage {
  const r = exactRecord(value, ['payment', 'resources']);
  return {
    payment: enumValue(r.payment, ['energy', 'mix', 'api', 'sub'], 'shortage payment'),
    resources: unique(
      r.resources,
      3,
      (value) => {
        const item = exactRecord(value, ['resource', 'required', 'available']);
        return {
          resource: enumValue(
            item.resource,
            ['energy', 'burst', 'sub', 'api'],
            'shortage resource',
          ),
          required: amount(item.required),
          available: amount(item.available),
        };
      },
      (item) => item.resource,
    ),
  };
}

export function overloadResources(
  events: readonly LikesEvent[],
  seat: Seat | null,
): ResourceShortage[] {
  const found = new Map<ResourceShortage['resource'], ResourceShortage>();
  for (const event of events) {
    if (event.kind !== 'overload' || event.seat !== seat) continue;
    const shortage = event.data.shortage;
    if (shortage !== undefined) {
      for (const resource of shortageValue(shortage).resources)
        found.set(resource.resource, resource);
    } else if (
      seat === null &&
      event.data.reason === 'shared-energy' &&
      typeof event.data.required === 'number' &&
      typeof event.data.available === 'number'
    ) {
      found.set('energy', {
        resource: 'energy',
        required: event.data.required,
        available: event.data.available,
      });
    }
  }
  return [...found.values()];
}
