import { describe, expect, it } from 'vitest';
import { ApiError, isForbidden, isStewardPermissionDenied } from '@shared/query/http';
import {
  defaultStewardPermissions,
  hasStewardPermission,
  normalizeStewardPermissions,
  stewardDonationScope,
  stewardPermissionKeys,
  canAccessStewardCharityManagement,
} from './stewardPermissions';
import { parseStewardLimitInput } from '../components/StewardPermissionEditor';

describe('per-user steward permissions', () => {
  it('keeps default review and history access while full diagnostic and delegation scopes stay absent', () => {
    const policy = defaultStewardPermissions(5);
    expect(stewardPermissionKeys).toHaveLength(51);
    expect(policy.permissions['ai.chat']).toBe(true);
    expect(policy.permissions['ai.review.read']).toBe(false);
    expect(policy.permissions['ai.memory.edit']).toBe(false);
    expect(policy.donation_scope).toBe('standard');
    expect(policy.permissions['endpoints.read']).toBe(false);
    expect(policy.permissions['endpoints.tags']).toBe(false);
    expect(policy.permissions['donations.review']).toBe(true);
    expect(policy.permissions['users.donations.read']).toBe(true);
    expect(policy.permissions['donations.read_all']).toBe(false);
    expect(policy.permissions['logs.unredacted']).toBe(false);
    expect(policy.permissions.automation).toBe(false);
  });

  it('honors explicitly removed grants without changing the account level', () => {
    const policy = defaultStewardPermissions(0);
    const user = { effective_level: 5, steward_permissions: policy };
    expect(hasStewardPermission(user, 'users.read')).toBe(false);
    expect(hasStewardPermission(user, 'donations.review')).toBe(false);
    expect(hasStewardPermission({ ...user, effective_level: 6 }, 'users.read')).toBe(true);
  });

  it('rejects missing or malformed grants instead of restoring defaults', () => {
    const policy = defaultStewardPermissions(5);
    expect(normalizeStewardPermissions(policy)).toEqual(policy);
    expect(() => normalizeStewardPermissions({ ...policy, donation_scope: undefined })).toThrow();
    expect(() =>
      normalizeStewardPermissions({ ...policy, donation_scope: 'everything' }),
    ).toThrow();
    const { ['users.read']: omitted, ...partial } = policy.permissions;
    expect(omitted).toBe(true);
    expect(() => normalizeStewardPermissions({ ...policy, permissions: partial })).toThrow();
    expect(() =>
      normalizeStewardPermissions({
        ...policy,
        permissions: { ...policy.permissions, 'users.read': 'true' },
      }),
    ).toThrow();
  });

  it('keeps donation scope independent from identity visibility and model targets', () => {
    const policy = defaultStewardPermissions(5);
    policy.donation_scope = 'enhanced';
    const user = { effective_level: 5, steward_permissions: policy };
    expect(stewardDonationScope(user)).toBe('enhanced');
    expect(hasStewardPermission(user, 'donations.read_all')).toBe(false);
    expect(hasStewardPermission(user, 'charity.non_mainstream')).toBe(false);
    policy.permissions['donations.read_all'] = true;
    policy.permissions['charity.non_mainstream'] = true;
    expect(stewardDonationScope(user)).toBe('enhanced');
    expect(stewardDonationScope({ ...user, effective_level: 6 })).toBe('full');
    expect(stewardDonationScope({ ...user, effective_level: 1 })).toBe('none');
  });

  it('opens charity management for granted actions without treating identity visibility as browse access', () => {
    const policy = defaultStewardPermissions(0);
    const user = { effective_level: 5, steward_permissions: policy };
    expect(canAccessStewardCharityManagement(user)).toBe(false);
    policy.permissions['donations.read_all'] = true;
    policy.donation_scope = 'full';
    expect(canAccessStewardCharityManagement(user)).toBe(false);
    policy.permissions['donations.manage_keys'] = true;
    policy.donation_scope = 'enhanced';
    expect(canAccessStewardCharityManagement(user)).toBe(true);
    policy.donation_scope = 'none';
    expect(canAccessStewardCharityManagement(user)).toBe(false);
    policy.permissions['charity.create'] = true;
    expect(canAccessStewardCharityManagement(user)).toBe(true);
    expect(canAccessStewardCharityManagement({ ...user, effective_level: 1 })).toBe(false);
    expect(canAccessStewardCharityManagement({ ...user, effective_level: 6 })).toBe(true);
  });

  it('preserves exact fractional point limits and enforces convenient input bounds', () => {
    expect(parseStewardLimitInput('users.balance_adjustment_max_milli', '1.001')).toBe('1001');
    expect(parseStewardLimitInput('users.balance_adjustment_max_milli', '9000000000000')).toBe(
      '9000000000000000',
    );
    expect(parseStewardLimitInput('users.balance_adjustment_max_milli', '1.0001')).toBeNull();
    expect(parseStewardLimitInput('users.rpm_limit_max', '4097')).toBeNull();
    expect(parseStewardLimitInput('users.rpm_limit_max', '')).toBeNull();
  });

  it('distinguishes a denied grant from a lost management session', () => {
    const denied = new ApiError('steward_permission_denied', 'Permission is not granted.', 403);
    expect(isStewardPermissionDenied(denied)).toBe(true);
    expect(isForbidden(denied)).toBe(false);
    expect(isForbidden(new ApiError('forbidden', 'The role is no longer valid.', 403))).toBe(true);
  });
});
