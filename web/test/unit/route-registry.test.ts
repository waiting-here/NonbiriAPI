import { describe, expect, test } from 'vitest';
import { ADMIN_PRIMARY_NAV } from '../../src/admin/navigation';
import { router as adminRouter } from '../../src/admin/routes';
import { USER_PRIMARY_NAV, STEWARD_NAV_ITEM } from '../../src/user/navigation';
import { router as userRouter } from '../../src/user/routes';
import {
  ADMIN_ROUTE_DESCRIPTORS,
  USER_ROUTE_DESCRIPTORS,
  relativeRoutePath,
  requiredRouteById,
  routePath,
  type RouteDescriptor,
} from '../../src/shared/routing/routeRegistry';

function childPaths(router: typeof userRouter): string[] {
  return (router.routes[0]?.children ?? [])
    .map((route) => route.path ?? (route.index ? '' : ''))
    .filter((path) => path !== '*');
}

function descriptorPath(path: string): string {
  return path === '' ? '/' : `/${path}`;
}

function assertRegisteredRoutes(
  paths: string[],
  descriptors: readonly RouteDescriptor[],
): void {
  for (const path of paths) {
    const descriptor = descriptors.find((route) => route.path === descriptorPath(path));
    expect(descriptor, `missing descriptor for ${path}`).toBeDefined();
    expect(descriptor?.registered, `unregistered route element for ${path}`).toBe(true);
  }
}

function assertDescriptorElements(
  paths: string[],
  descriptors: readonly RouteDescriptor[],
): void {
  for (const descriptor of descriptors.filter((route) => route.registered)) {
    const relativePath = descriptor.path === '/' ? '' : descriptor.path.slice(1);
    expect(paths, `registered descriptor missing element for ${descriptor.id}`).toContain(relativePath);
  }
}

describe('canonical route registry wiring', () => {
  test('every router element is represented by a registered descriptor', () => {
    const userPaths = childPaths(userRouter);
    const adminPaths = childPaths(adminRouter);
    assertRegisteredRoutes(userPaths, USER_ROUTE_DESCRIPTORS);
    assertRegisteredRoutes(adminPaths, ADMIN_ROUTE_DESCRIPTORS);
    assertDescriptorElements(userPaths, USER_ROUTE_DESCRIPTORS);
    assertDescriptorElements(adminPaths, ADMIN_ROUTE_DESCRIPTORS);
  });

  test('visible shell navigation never points at an unregistered route', () => {
    const userPaths = childPaths(userRouter);
    const adminPaths = childPaths(adminRouter);
    for (const item of [...USER_PRIMARY_NAV, STEWARD_NAV_ITEM]) {
      expect(userPaths, item.to).toContain(item.to.slice(1));
    }
    for (const item of ADMIN_PRIMARY_NAV) {
      expect(adminPaths, item.to).toContain(item.to.slice(1));
    }
  });

  test('required path helpers fail closed for unknown IDs and parameters', () => {
    expect(() => requiredRouteById('user', 'not-a-route')).toThrow('Unknown user route descriptor');
    expect(() => relativeRoutePath('admin', 'not-a-route')).toThrow('Unknown admin route descriptor');
    expect(routePath('user', 'home')).toBe('/');
    expect(routePath('admin', 'admin-report-detail', { caseId: 'case/opaque' })).toBe('/reports/case%2Fopaque');
    expect(() => routePath('admin', 'admin-report-detail')).toThrow('Missing route parameter: caseId');
    expect(() => routePath('admin', 'admin-report-detail', { caseId: '' })).toThrow('Missing route parameter: caseId');
  });
});
