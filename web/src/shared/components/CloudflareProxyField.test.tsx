import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CloudflareProxyField } from './CloudflareProxyField';
import type { CloudflareProxyMode } from '@shared/cloudflareProxy';

function Editor({ mode }: { mode: CloudflareProxyMode }) {
  const [enabled, setEnabled] = useState(true);
  return <CloudflareProxyField enabled={enabled} mode={mode} onChange={setEnabled} />;
}
describe('Cloudflare forwarding status', () => {
  it.each([
    [
      'direct',
      'No forwarding service is configured. Requests currently connect directly to upstreams.',
    ],
    [
      'legacy',
      'Legacy endpoint routes only: forwarding applies to matching upstreams. Ask an administrator to configure a universal Worker.',
    ],
    [
      'worker',
      'A universal Cloudflare Worker is configured. Requests use it when forwarding is on.',
    ],
  ] as const)('reports %s mode separately from the switch', async (mode, description) => {
    const view = await renderWithProviders(<Editor mode={mode} />, { station: 'user' });
    const toggle = screen.getByRole('switch', { name: 'Cloudflare forwarding' });
    expect(toggle).toBeChecked();
    expect(toggle).toHaveAccessibleDescription(description);
    await view.user.click(toggle);
    expect(toggle).not.toBeChecked();
    expect(toggle).toHaveAccessibleDescription('Off. Requests connect directly to upstreams.');
  });
});
