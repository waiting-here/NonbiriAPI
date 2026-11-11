import { decoded, idempotentOptions } from './api';
import { array, decimal, decimalID, integer, record, string } from './wire';
import type { ManagementRole } from './managedUsers';

export interface ManagedResource {
  id: string;
  name: string;
  name_en: string;
  group: string;
  precision: number;
  maximum: string;
  quantity: string;
  minimum: string;
  capacity: string;
}
export interface ResourceAdjustment {
  asset: string;
  direction: 'increase' | 'decrease';
  quantity: string;
  expected_quantity: string;
  reason: string;
}

function resources(value: unknown): ManagedResource[] {
  return array(value, 'user resources').map((item) => {
    const r = record(
      item,
      ['id', 'name', 'name_en', 'group', 'precision', 'maximum', 'quantity', 'minimum', 'capacity'],
      'resource',
    );
    return {
      id: string(r.id, 'resource id'),
      name: string(r.name, 'resource name'),
      name_en: string(r.name_en, 'resource name'),
      group: string(r.group, 'resource group'),
      precision: integer(r.precision, 'resource precision', 0, 0),
      maximum: decimal(r.maximum, 'adjustment maximum'),
      quantity: decimal(r.quantity, 'resource quantity'),
      minimum: decimal(r.minimum, 'resource minimum'),
      capacity: decimal(r.capacity, 'resource capacity'),
    };
  });
}

const path = (role: ManagementRole, id: string) =>
  `${role === 'admin' ? '/admin/api' : '/api/steward'}/users/${decimalID(id, 'user id')}/resources`;

export function getManagedResources(role: ManagementRole, id: string, signal?: AbortSignal) {
  return decoded(
    path(role, id),
    (value) => resources(record(value, ['resources'], 'user resources').resources),
    { signal },
  );
}

export function adjustManagedResource(
  role: ManagementRole,
  id: string,
  input: ResourceAdjustment,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    path(role, id),
    (value) => {
      const result = record(value, ['resources', 'changes'], 'resource adjustment');
      return {
        resources: resources(result.resources),
        changes: array(result.changes, 'resource changes').map((value) => {
          const change = record(value, ['field', 'before', 'after'], 'resource change');
          return {
            field: string(change.field, 'field'),
            before: string(change.before, 'before'),
            after: string(change.after, 'after'),
          };
        }),
      };
    },
    idempotentOptions(key, { method: 'PATCH', json: input, signal }),
  );
}

export function resourceAdjustmentPreview(
  resource: ManagedResource,
  direction: ResourceAdjustment['direction'],
  quantity: string,
): string | null {
  if (quantity.length > 40 || !/^[1-9][0-9]*$/.test(quantity)) return null;
  const amount = BigInt(quantity);
  if (amount > BigInt(resource.maximum)) return null;
  const next = BigInt(resource.quantity) + (direction === 'increase' ? amount : -amount);
  return next >= BigInt(resource.minimum) && next <= BigInt(resource.capacity)
    ? next.toString()
    : null;
}
