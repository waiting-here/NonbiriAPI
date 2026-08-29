import { describe, expect, test } from 'vitest';
import { ApiError } from '@shared/query/http';
import { classifyRouteError } from './RouteErrorPage';

describe('route error classification', () => {
  test('keeps forbidden, missing, server, network, and render failures distinct', () => {
    expect(classifyRouteError(new ApiError('forbidden', 'no', 403))).toBe('403');
    expect(classifyRouteError(new ApiError('not_found', 'no', 404))).toBe('404');
    expect(classifyRouteError(new ApiError('internal', 'no', 500))).toBe('500');
    expect(classifyRouteError(new ApiError('network_error', 'no', 0))).toBe('network');
    expect(classifyRouteError(new ApiError('maintenance', 'no', 503))).toBe('maintenance');
    expect(classifyRouteError(new ApiError('service_unavailable', 'no', 503))).toBe('500');
    expect(classifyRouteError(new ApiError('service_unavailable', 'no', 500))).toBe('500');
    expect(classifyRouteError(new Error('implementation detail'))).toBe('render');
  });
});
