import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import { DonationSourceContext } from './DonationSourceContext';
import type { ManagedSafeSource } from '@shared/operations/charity';

const source: ManagedSafeSource = {
  kind: 'mainstream',
  connector_type: 'openai-compatible',
  base_url: 'https://synthetic.test/v1',
  channel_id: 'mch_AAAAAAAAAAAAAAAAAAAAAA',
  name: 'Snapshot channel',
};
describe('Donation source context', () => {
  it.each([
    ['subscription', common.common.operations.charity.mainstreamSubscription],
    ['api_platform', common.common.operations.charity.mainstreamApiPlatform],
    ['future-category', common.common.operations.charity.mainstreamSource],
  ])('shows the %s source classification', async (category, label) => {
    await renderWithProviders(
      <DonationSourceContext source={{ ...source, category }} showAddress />,
      { station: 'admin', role: 'admin' },
    );
    expect(screen.getByText(label.replace('{{name}}', 'Snapshot channel'))).toBeInTheDocument();
    expect(screen.getByText(source.base_url)).toBeInTheDocument();
    expect(
      screen.queryByText(common.common.operations.charity.currentEndpointTags),
    ).not.toBeInTheDocument();
  });
  it('shows only the tags supplied for this endpoint with the shared colors', async () => {
    await renderWithProviders(
      <DonationSourceContext
        showAddress
        source={{
          ...source,
          category: 'subscription',
          endpoint_tags: ['mainstream', 'user_gateway'],
        }}
      />,
      { station: 'user', role: 'level5' },
    );
    const group = screen.getByLabelText(common.common.operations.charity.currentEndpointTags);
    expect(group.querySelectorAll('.nb-badge')).toHaveLength(2);
    expect(group.querySelector('.nb-badge--warn')).toBeInTheDocument();
    expect(group.querySelector('.nb-badge--info')).toBeInTheDocument();
  });
  it('keeps a custom endpoint without labels free of empty tag placeholders', async () => {
    await renderWithProviders(
      <DonationSourceContext
        showAddress
        source={{
          kind: 'custom',
          connector_type: 'openai-compatible',
          base_url: 'https://custom.test/v1',
          endpoint_tags: [],
        }}
      />,
      { station: 'user', role: 'level6' },
    );
    expect(screen.getByText(/custom.test/)).toBeInTheDocument();
    expect(
      screen.queryByLabelText(common.common.operations.charity.currentEndpointTags),
    ).not.toBeInTheDocument();
  });
});
