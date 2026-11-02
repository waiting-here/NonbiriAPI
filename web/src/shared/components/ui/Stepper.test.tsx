import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Stepper } from './Stepper';

describe('Stepper', () => {
  it('marks only the current step and shows its action', () => {
    render(
      <Stepper
        label="Get started"
        doneLabel="Done"
        steps={[
          { id: 'key', title: 'Create a key', state: 'done', doneAction: <a href="/keys">View</a> },
          {
            id: 'model',
            title: 'Choose a model',
            state: 'current',
            action: <button type="button">Browse charity models</button>,
          },
          { id: 'client', title: 'Fill in your client', state: 'todo' },
        ]}
      />,
    );
    const list = screen.getByRole('list', { name: 'Get started' });
    const items = within(list).getAllByRole('listitem');
    expect(items.map((item) => item.getAttribute('aria-current'))).toEqual([null, 'step', null]);
    expect(within(items[0]).getByRole('img', { name: 'Done' })).toBeInTheDocument();
    expect(within(items[0]).getByRole('link', { name: 'View' })).toBeVisible();
    expect(within(items[1]).getByRole('button', { name: 'Browse charity models' })).toBeVisible();
    expect(within(items[2]).getByText('3')).toBeInTheDocument();
  });
});
