import { useState } from 'react';
import { fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { likesCatalog } from './catalog';
import { catalogWire } from './testCatalog';
import { initialSelection } from './selection';
import { LoadoutEditor, LoadoutStep, type LoadoutStepID } from './Loadout';

const catalog = likesCatalog(catalogWire()).modes.quick;
describe('loadout disclosure', () => {
  beforeEach(() =>
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: false })),
    ),
  );
  afterEach(() => vi.unstubAllGlobals());
  it('keeps one step open and preserves a changed selection while switching steps', async () => {
    const change = vi.fn();
    function View() {
      const [active, setActive] = useState<LoadoutStepID | null>('mode');
      const [value, setValue] = useState(initialSelection(catalog));
      const disclosure = {
        active,
        onToggle: (id: LoadoutStepID, open: boolean) =>
          setActive((previous) => (open ? id : previous === id ? null : previous)),
      };
      return (
        <>
          <LoadoutStep id="mode" title="Mode" summary="Quick" disclosure={disclosure}>
            <p>Quick</p>
          </LoadoutStep>
          <LoadoutEditor
            catalog={catalog}
            value={value}
            disabled={false}
            onInspect={vi.fn()}
            disclosure={disclosure}
            onChange={(next) => {
              change(next);
              setValue(next);
            }}
          />
        </>
      );
    }
    const view = await renderWithProviders(<View />, { station: 'user' });
    const steps = view.container.querySelectorAll<HTMLDetailsElement>('.likes-loadout-step');
    expect(steps).toHaveLength(4);
    steps[1].open = true;
    fireEvent(steps[1], new Event('toggle'));
    expect([...steps].filter((step) => step.open)).toHaveLength(1);
    const role = catalog.roles[1];
    await view.user.click(
      view.container.querySelector<HTMLButtonElement>(`[data-guide="role:${role.id}"]`)!,
    );
    expect(change).toHaveBeenCalledWith({
      role: role.id,
      harness: null,
      skills: [...catalog.loadouts.find((p) => p.role === role.id)!.skills],
    });
    steps[2].open = true;
    fireEvent(steps[2], new Event('toggle'));
    expect([...steps].filter((step) => step.open)).toHaveLength(1);
    expect(steps[1].querySelector('summary')).toHaveTextContent(role.name);
    expect(
      view.container.querySelector<HTMLButtonElement>(`[data-guide="role:${role.id}"]`)!,
    ).toHaveAttribute('aria-pressed', 'true');
  });
  it('keeps the existing flat editor for consumers without disclosure', async () => {
    const view = await renderWithProviders(
      <LoadoutEditor
        catalog={catalog}
        value={initialSelection(catalog)}
        disabled={false}
        onChange={vi.fn()}
        onInspect={vi.fn()}
      />,
      { station: 'user' },
    );
    expect(view.container.querySelector('.likes-loadout-step')).toBeNull();
    expect(screen.getAllByRole('checkbox').length).toBeGreaterThan(0);
  });
});
