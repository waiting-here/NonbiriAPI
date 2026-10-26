import { screen } from '@testing-library/react';
import { useLocation } from 'react-router';
import { expect, it } from 'vitest';
import { ArtworkPolicyProvider } from '@shared/artwork/policy';
import { renderWithProviders } from '../../../../test/unit/support';
import { GachaPoolEntries } from './GachaEntry';
import type { GachaPoolEntry } from './entry';

const ratedPool: GachaPoolEntry = {
  id: 'rated/pool',
  name: '糖夜迷藏',
  name_en: 'Sweet Hide-and-Seek',
  cover: 's',
  featured: ['s-chatgpt', 's-claude'],
  starts_at: 1_800_000_000,
  ends_at: 1_800_086_400,
};
const generalPool: GachaPoolEntry = {
  ...ratedPool,
  id: 'general',
  name: '万象化身',
  name_en: 'Myriad forms',
  cover: 'h',
  featured: ['h-chatgpt'],
};

function Entries({
  disabled = false,
  pools = [ratedPool],
}: {
  disabled?: boolean;
  pools?: readonly GachaPoolEntry[];
}) {
  const location = useLocation();
  return (
    <ArtworkPolicyProvider disabled={disabled}>
      <GachaPoolEntries pools={pools} />
      <output aria-label="Location">{location.pathname + location.search}</output>
    </ArtworkPolicyProvider>
  );
}

it.each(['en', 'zh'] as const)(
  'starts the R-18 cover blurred and separates its toggle from navigation in %s',
  async (locale) => {
    const view = await renderWithProviders(<Entries />, {
      station: 'user',
      locale,
      route: '/activities',
    });
    const cover = screen.getByRole('button', {
      name: locale === 'en' ? 'Reveal R-18 cover: Sweet Hide-and-Seek' : '显示 R-18 封面：糖夜迷藏',
    });
    expect(cover).toHaveAttribute('aria-pressed', 'false');
    expect(cover.closest('a')).toBeNull();
    expect(
      screen.getByText(locale === 'en' ? 'R-18 · Click to reveal cover' : 'R-18 · 点击显示封面'),
    ).toBeVisible();
    await view.user.click(cover);
    expect(cover).toHaveAttribute('aria-pressed', 'true');
    expect(
      screen.getByText(locale === 'en' ? 'R-18 · Click to blur cover' : 'R-18 · 点击模糊封面'),
    ).toBeVisible();
    expect(screen.getByLabelText('Location')).toHaveTextContent(/^\/activities$/);
    await view.user.keyboard(' ');
    expect(cover).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByLabelText('Location')).toHaveTextContent(/^\/activities$/);

    const textLink = screen.getByRole('link');
    expect(textLink).toHaveAttribute('href', '/activities/gacha?pool=rated%2Fpool');
    expect(textLink.querySelector('button')).toBeNull();
    await view.user.click(textLink);
    expect(screen.getByLabelText('Location')).toHaveTextContent(
      '/activities/gacha?pool=rated%2Fpool',
    );
  },
);

it('starts blurred again after leaving and mounting the entries again', async () => {
  const first = await renderWithProviders(<Entries />, { station: 'user', route: '/activities' });
  await first.user.click(screen.getByRole('button', { name: /Reveal R-18 cover/ }));
  expect(screen.getByRole('button', { name: /Blur R-18 cover/ })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  first.unmount();

  await renderWithProviders(<Entries />, { station: 'user', route: '/activities' });
  expect(screen.getByRole('button', { name: /Reveal R-18 cover/ })).toHaveAttribute(
    'aria-pressed',
    'false',
  );
});

it.each([false, true])(
  'keeps the ordinary banner as one cover-and-copy link with R-18 disabled: %s',
  async (disabled) => {
    const view = await renderWithProviders(<Entries disabled={disabled} pools={[generalPool]} />, {
      station: 'user',
      route: '/activities',
    });
    const link = screen.getByRole('link');
    const cover = screen.getByRole('img', { name: 'Myriad forms' });
    expect(screen.queryByRole('button')).toBeNull();
    expect(cover.closest('a')).toBe(link);
    expect(link).toHaveAttribute('href', '/activities/gacha?pool=general');
    await view.user.click(cover);
    expect(screen.getByLabelText('Location')).toHaveTextContent('/activities/gacha?pool=general');
  },
);

it('keeps a disabled R-18 cover as a neutral placeholder and cannot reveal it', async () => {
  const view = await renderWithProviders(<Entries disabled />, {
    station: 'user',
    route: '/activities',
  });
  const cover = screen.getByRole('button', { name: 'R-18 cover unavailable: Sweet Hide-and-Seek' });
  expect(cover).toBeDisabled();
  expect(cover).toHaveAttribute('aria-pressed', 'false');
  expect(view.container.querySelector('img')).toBeNull();
  expect(screen.getByRole('img', { name: 'Sweet Hide-and-Seek' })).toBeVisible();
  await view.user.click(cover);
  expect(cover).toHaveAttribute('aria-pressed', 'false');
  expect(view.container.querySelector('img')).toBeNull();
  expect(screen.getByLabelText('Location')).toHaveTextContent(/^\/activities$/);
  expect(screen.getByRole('link')).toHaveAttribute('href', '/activities/gacha?pool=rated%2Fpool');
});

it('removes an already revealed image as soon as the site policy disables R-18', async () => {
  const view = await renderWithProviders(<Entries />, { station: 'user', route: '/activities' });
  await view.user.click(screen.getByRole('button', { name: /Reveal R-18 cover/ }));
  expect(view.container.querySelector('img')).not.toBeNull();
  view.rerender(<Entries disabled />);
  expect(view.container.querySelector('img')).toBeNull();
  expect(screen.getByRole('button', { name: /R-18 cover unavailable/ })).toBeDisabled();
  expect(screen.getByRole('button', { name: /R-18 cover unavailable/ })).toHaveAttribute(
    'aria-pressed',
    'false',
  );
});
