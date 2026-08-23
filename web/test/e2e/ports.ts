function portBase(raw: string | undefined): number {
  const parsed = raw ? Number(raw) : 43_90;
  if (!Number.isInteger(parsed) || parsed < 1_024 || parsed > 65_533) {
    throw new Error('NONBIRI_E2E_PORT_BASE must be an integer from 1024 through 65533.');
  }
  return parsed;
}

export const E2E_PORT_BASE = portBase(process.env.NONBIRI_E2E_PORT_BASE);
export const ADMIN_ORIGIN = `http://127.0.0.1:${E2E_PORT_BASE}`;
export const USER_ORIGIN = `http://127.0.0.1:${E2E_PORT_BASE + 1}`;
export const FIXTURE_ORIGIN = `http://127.0.0.1:${E2E_PORT_BASE + 2}`;
