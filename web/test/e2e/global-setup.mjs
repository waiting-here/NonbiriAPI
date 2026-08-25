import { startFixtureServers } from './server.mjs';

function portBase(raw) {
  const parsed = raw ? Number(raw) : 43_90;
  if (!Number.isInteger(parsed) || parsed < 1_024 || parsed > 65_533) {
    throw new Error('NONBIRI_E2E_PORT_BASE must be an integer from 1024 through 65533.');
  }
  return parsed;
}

export default async function globalSetup() {
  const base = portBase(process.env.NONBIRI_E2E_PORT_BASE);
  return startFixtureServers([base, base + 1, base + 2]);
}
