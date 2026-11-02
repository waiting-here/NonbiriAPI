import { fireEvent, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { OverlayPortal } from '@shared/components/OverlayPortal';
import { DuelDialog } from './duel/Dialog';
import { GameRulesDialog } from './GameRulesDialog';

const fullscreenDescriptor = Object.getOwnPropertyDescriptor(document, 'fullscreenElement');
afterEach(() => {
  vi.restoreAllMocks();
  if (fullscreenDescriptor)
    Object.defineProperty(document, 'fullscreenElement', fullscreenDescriptor);
  else Reflect.deleteProperty(document, 'fullscreenElement');
  document.querySelector('[data-fullscreen-test]')?.remove();
});

function Rules() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Open rules</button>
      <GameRulesDialog
        open={open}
        title="Game rules"
        closeLabel="Close rules"
        onClose={() => setOpen(false)}
        sections={[{ title: 'Play', paragraphs: ['Choose a mode.'] }]}
      />
    </>
  );
}

it.each([false, true])(
  'focuses rules and returns to its trigger (fullscreen %s)',
  async (fullscreen) => {
    let scene: HTMLElement | undefined;
    if (fullscreen) {
      scene = document.createElement('div');
      scene.dataset.fullscreenTest = '';
      document.body.append(scene);
      Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: scene });
    }
    const view = await renderWithProviders(<Rules />, { station: 'user', role: 'user' });
    const trigger = screen.getByRole('button', { name: 'Open rules' });
    await view.user.click(trigger);
    const dialog = screen.getByRole('dialog', { name: 'Game rules' });
    const close = within(dialog).getByRole('button', { name: 'Close rules' });
    expect(dialog).toHaveProperty('open', true);
    expect(close).toHaveFocus();
    if (scene) expect(scene).toContainElement(dialog);
    await view.user.tab();
    expect(close).toHaveFocus();
    await view.user.tab({ shift: true });
    expect(close).toHaveFocus();
    fireEvent(dialog, new Event('cancel', { bubbles: false, cancelable: true }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  },
);

it('cycles focus between the close control and tutorial actions', async () => {
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockImplementation(function () {
    return [new DOMRect(0, 0, 40, 40)] as unknown as DOMRectList;
  });
  function Tutorial() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button onClick={() => setOpen(true)}>Start tutorial</button>
        {open ? (
          <OverlayPortal>
            <DuelDialog title="Tutorial" onClose={() => setOpen(false)}>
              <button>Next</button>
              <button>Finish</button>
            </DuelDialog>
          </OverlayPortal>
        ) : null}
      </>
    );
  }
  const view = await renderWithProviders(<Tutorial />, { station: 'user', role: 'user' });
  const trigger = screen.getByRole('button', { name: 'Start tutorial' });
  await view.user.click(trigger);
  const dialog = screen.getByRole('dialog', { name: 'Tutorial' });
  const close = within(dialog).getByRole('button', { name: 'Close' });
  const finish = within(dialog).getByRole('button', { name: 'Finish' });
  expect(close).toHaveFocus();
  await view.user.tab({ shift: true });
  expect(finish).toHaveFocus();
  await view.user.tab();
  expect(close).toHaveFocus();
  await view.user.click(close);
  expect(trigger).toHaveFocus();
});

it('keeps clicks inside the panel open and closes on a backdrop click', async () => {
  const view = await renderWithProviders(<Rules />, { station: 'user', role: 'user' });
  const trigger = screen.getByRole('button', { name: 'Open rules' });
  await view.user.click(trigger);
  const dialog = screen.getByRole('dialog', { name: 'Game rules' });
  vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue(new DOMRect(20, 20, 200, 160));
  fireEvent.click(dialog, { clientX: 40, clientY: 40 });
  expect(dialog).toBeInTheDocument();
  fireEvent.click(dialog, { clientX: 10, clientY: 10 });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});
