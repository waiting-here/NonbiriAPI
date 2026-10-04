import { useState } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  Affix,
  DataTable,
  Field,
  FilterBar,
  Fold,
  MoreMenu,
  Note,
  OptionCards,
  Panel,
  PanelBody,
  PanelFoot,
  PanelHead,
  SaveBar,
  Segmented,
  Tabs,
  Toggle,
} from './index';

afterEach(() => sessionStorage.clear());

describe('shared UI patterns', () => {
  it('gives panels a named region, heading, body controls and footer actions', () => {
    render(
      <Panel aria-label="Account" tone="danger">
        <PanelHead
          title="Delete account"
          description="Review before continuing"
          level={3}
          actions={<button>History</button>}
        />
        <PanelBody>
          <label>
            Email
            <input />
          </label>
        </PanelBody>
        <PanelFoot>
          <button>Delete</button>
        </PanelFoot>
      </Panel>,
    );
    const panel = screen.getByRole('region', { name: 'Account' });
    expect(within(panel).getByRole('heading', { name: 'Delete account', level: 3 })).toBeVisible();
    expect(within(panel).getByRole('textbox', { name: 'Email' })).toBeVisible();
    expect(
      within(panel).getByRole('button', { name: 'Delete' }).closest('.nb-panel__foot'),
    ).not.toBeNull();
    expect(within(panel).getByRole('button', { name: 'History' })).toBeVisible();
  });

  it('preserves drafts across folding and stores only the boolean open state', async () => {
    const user = userEvent.setup();
    const view = render(
      <Fold title="Advanced" summary="Optional settings" meta="2 fields" persistKey="advanced">
        <label>
          Retry
          <input />
        </label>
      </Fold>,
    );
    expect(screen.getByRole('textbox', { name: 'Retry' })).not.toBeVisible();
    await user.click(screen.getByText('Advanced'));
    const draft = await screen.findByRole('textbox', { name: 'Retry' });
    expect(draft).toBeVisible();
    await user.type(draft, '3 attempts');
    await user.click(screen.getByText('Advanced'));
    await waitFor(() => expect(sessionStorage.getItem('nb.fold.advanced')).toBe('0'));
    await user.click(screen.getByText('Advanced'));
    expect(await screen.findByRole('textbox', { name: 'Retry' })).toHaveValue('3 attempts');
    await waitFor(() => expect(sessionStorage.getItem('nb.fold.advanced')).toBe('1'));
    expect(sessionStorage.length).toBe(1);
    view.unmount();
    render(
      <Fold title="Advanced" persistKey="advanced">
        <label>
          Retry
          <input />
        </label>
      </Fold>,
    );
    expect(screen.getByRole('textbox', { name: 'Retry' })).toBeVisible();
    await user.click(screen.getByText('Advanced'));
    await waitFor(() => expect(sessionStorage.getItem('nb.fold.advanced')).toBe('0'));
    expect(screen.getByRole('textbox', { name: 'Retry' })).not.toBeVisible();
  });

  it('links labels, help, errors and input units', () => {
    render(
      <Field
        label="Timeout"
        optional="Optional"
        help="Choose a duration"
        error="Enter a positive value"
      >
        {(props) => <Affix {...props} unit="seconds" type="number" className="duration" />}
      </Field>,
    );
    const input = screen.getByRole('spinbutton', { name: 'Timeout Optional' });
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Enter a positive value Choose a duration seconds');
    expect(input).toHaveClass('nb-input', 'duration');
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a positive value');
    for (const id of input.getAttribute('aria-describedby')!.split(' '))
      expect(document.getElementById(id)).not.toBeNull();
  });

  it('announces toggle state and keeps description separate from its name', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <Toggle label="Enabled" description="Accept calls" checked={false} onChange={onChange} />,
    );
    const toggle = screen.getByRole('switch', { name: 'Enabled' });
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    expect(toggle).toHaveAccessibleDescription('Accept calls');
    await user.click(toggle);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('uses native radio groups for segmented choices', async () => {
    const user = userEvent.setup();
    function Choices() {
      const [value, setValue] = useState('light');
      return (
        <Segmented
          label="Theme"
          value={value}
          onChange={setValue}
          options={[
            { value: 'light', label: 'Light' },
            { value: 'dark', label: 'Dark' },
          ]}
        />
      );
    }
    render(<Choices />);
    const group = screen.getByRole('radiogroup', { name: 'Theme' });
    const light = within(group).getByRole('radio', { name: 'Light' });
    expect(light).toBeChecked();
    light.focus();
    await user.keyboard('{ArrowRight}');
    expect(within(group).getByRole('radio', { name: 'Dark' })).toBeChecked();
  });

  it('names option cards and announces their descriptions and disabled state', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <OptionCards
        legend="Source"
        value="a"
        onChange={onChange}
        options={[
          { value: 'a', title: 'Existing', body: 'Use a saved service' },
          { value: 'b', title: 'New', body: 'Add a service' },
          { value: 'c', title: 'Unavailable', disabled: true },
        ]}
      />,
    );
    const group = screen.getByRole('group', { name: 'Source' });
    expect(within(group).getByRole('radio', { name: 'Existing' })).toHaveAccessibleDescription(
      'Use a saved service',
    );
    expect(within(group).getByRole('radio', { name: 'Unavailable' })).toBeDisabled();
    await user.click(within(group).getByRole('radio', { name: 'New' }));
    expect(onChange).toHaveBeenCalledWith('b');
  });

  it('moves menu focus, skips disabled actions, wraps and restores the trigger on Escape', async () => {
    const user = userEvent.setup();
    render(
      <MoreMenu
        label="More service actions"
        items={[
          { label: 'Edit', onSelect: vi.fn() },
          { label: 'Disabled', disabled: true, onSelect: vi.fn() },
          'separator',
          { label: 'Delete', danger: true, onSelect: vi.fn() },
        ]}
      />,
    );
    const trigger = screen.getByRole('button', { name: 'More service actions' });
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu');
    await user.click(trigger);
    const edit = screen.getByRole('menuitem', { name: 'Edit' });
    await waitFor(() => expect(edit).toHaveFocus());
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(edit).toHaveFocus();
    await user.keyboard('{ArrowUp}');
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(trigger).toHaveFocus();
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger.closest('details')).not.toHaveAttribute('open');
  });

  it('opens a menu by keyboard and closes after selection or an outside pointer', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(
      <>
        <MoreMenu label="More" items={[{ label: 'Edit', onSelect }]} />
        <button>Outside</button>
      </>,
    );
    const trigger = screen.getByRole('button', { name: 'More' });
    trigger.focus();
    await user.keyboard('{ArrowDown}');
    await waitFor(() => expect(screen.getByRole('menuitem', { name: 'Edit' })).toHaveFocus());
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(trigger).toHaveFocus();
    await user.click(trigger);
    await waitFor(() => expect(screen.getByRole('menuitem', { name: 'Edit' })).toHaveFocus());
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Outside' }));
    expect(trigger.closest('details')).not.toHaveAttribute('open');
    expect(trigger).toHaveFocus();
  });

  it('renders the save region only for changes and disables actions while busy', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    const onDiscard = vi.fn();
    const props = {
      onSave,
      onDiscard,
      saveLabel: 'Save',
      discardLabel: 'Discard',
      dirtyLabel: (count: number) => `${count} changes`,
    };
    const view = render(<SaveBar {...props} dirtyCount={0} />);
    expect(screen.queryByRole('region')).not.toBeInTheDocument();
    view.rerender(<SaveBar {...props} dirtyCount={2} />);
    expect(screen.getByRole('region', { name: '2 changes' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    expect(onDiscard).toHaveBeenCalledTimes(1);
    view.rerender(<SaveBar {...props} dirtyCount={2} saveDisabled />);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeEnabled();
    view.rerender(<SaveBar {...props} dirtyCount={2} busy />);
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeDisabled();
  });

  it.each(['info', 'warn', 'bad', 'ok'] as const)(
    'announces a %s notice with its action',
    (tone) => {
      render(
        <Note tone={tone} title="Result" action={<button>Retry</button>}>
          Next step
        </Note>,
      );
      const note = screen.getByRole(tone === 'warn' || tone === 'bad' ? 'alert' : 'status');
      expect(note).toHaveTextContent('Result');
      expect(note).toHaveTextContent('Next step');
      expect(within(note).getByRole('button', { name: 'Retry' })).toBeVisible();
    },
  );

  it('navigates tabs with wrapping arrows and Home/End and links panels', async () => {
    const user = userEvent.setup();
    function Sections() {
      const [value, setValue] = useState('one');
      return (
        <Tabs
          label="Records"
          value={value}
          onChange={setValue}
          tabs={[
            { value: 'one', label: 'Calls', panelId: 'calls', id: 'calls-tab', count: 0 },
            { value: 'two', label: 'Credits' },
            { value: 'three', label: 'Errors' },
          ]}
        />
      );
    }
    render(<Sections />);
    const tabs = within(screen.getByRole('tablist', { name: 'Records' })).getAllByRole('tab');
    expect(tabs[0]).toHaveAttribute('aria-controls', 'calls');
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true');
    tabs[0]!.focus();
    await user.keyboard('{ArrowRight}');
    expect(tabs[1]).toHaveFocus();
    expect(tabs[1]).toHaveAttribute('aria-selected', 'true');
    expect(tabs[0]).toHaveAttribute('tabindex', '-1');
    await user.keyboard('{End}');
    expect(tabs[2]).toHaveFocus();
    await user.keyboard('{ArrowRight}');
    expect(tabs[0]).toHaveFocus();
    await user.keyboard('{ArrowLeft}');
    expect(tabs[2]).toHaveFocus();
    await user.keyboard('{Home}');
    expect(tabs[0]).toHaveFocus();
  });

  it('renders a named table with column headings and mobile cell roles', () => {
    render(
      <DataTable
        caption="Services"
        rows={[{ id: '1', name: 'Example', count: 12 }]}
        rowKey={(row) => row.id}
        selectedKey="1"
        columns={[
          {
            key: 'name',
            header: 'Name',
            cell: 'title',
            mobileLabel: 'Service',
            render: (row) => row.name,
          },
          {
            key: 'count',
            header: 'Calls',
            cell: 'meta',
            align: 'num',
            mobileLabel: 'Calls',
            render: (row) => row.count,
          },
        ]}
      />,
    );
    const table = screen.getByRole('table', { name: 'Services' });
    expect(within(table).getByRole('columnheader', { name: 'Calls' })).toHaveAttribute(
      'scope',
      'col',
    );
    expect(within(table).getByRole('cell', { name: 'Example' })).toHaveAttribute(
      'data-cell',
      'title',
    );
    const count = within(table).getByRole('cell', { name: '12' });
    expect(count).toHaveAttribute('data-cell', 'meta');
    expect(count).toHaveAttribute('data-label', 'Calls');
    expect(count.closest('tr')).toHaveAttribute('aria-selected', 'true');
  });

  it('submits search and provides named filter removal actions', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    const onRemove = vi.fn();
    const onClearAll = vi.fn();
    render(
      <FilterBar
        search={
          <>
            <label>
              Search
              <input />
            </label>
            <button>Search</button>
          </>
        }
        secondary={
          <label>
            State
            <select>
              <option>All</option>
            </select>
          </label>
        }
        secondaryLabel="Filters"
        activeCount={1}
        onSubmit={onSubmit}
        chips={[{ key: 'state', label: 'Active', onRemove, removeLabel: 'Remove active filter' }]}
        onClearAll={onClearAll}
        clearAllLabel="Clear filters"
      />,
    );
    await user.type(screen.getByRole('textbox', { name: 'Search' }), 'example{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
    await user.click(screen.getByText('Filters · 1'));
    expect(screen.getByRole('combobox', { name: 'State' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Remove active filter' }));
    expect(onRemove).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(onClearAll).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});
