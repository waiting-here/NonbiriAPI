import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { createMemoryRouter, Link, RouterProvider, useNavigate } from 'react-router';
import { beforeAll, expect, it } from 'vitest';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from '@shared/i18n/common/en.json';
import { DraftLeaveGuard, DraftLeaveScope } from './DraftLeaveScope';

beforeAll(async () => {
  await i18n.use(initReactI18next).init({ resources: { en: { translation: en } }, lng: 'en' });
});

function Editor({ outcome }: { outcome: 'saved' | 'failed' | 'unknown' }) {
  const navigate = useNavigate();
  const [dirty, setDirty] = useState(true);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (saved) void navigate('/next?binding_key=11&donations_page=2');
  }, [saved, navigate]);
  return (
    <>
      <DraftLeaveGuard dirty={dirty} />
      <button
        onClick={() => {
          if (outcome !== 'saved') {
            setError(outcome);
            return;
          }
          setDirty(false);
          setSaved(true);
        }}
      >
        Save
      </button>
      <Link to="/next">Other page</Link>
      {error ? <p role="alert">{error}</p> : null}
    </>
  );
}

function setup(outcome: 'saved' | 'failed' | 'unknown', scoped = true, anotherDraft = false) {
  const editor = <Editor outcome={outcome} />;
  const router = createMemoryRouter([
    {
      path: '/',
      element: scoped ? (
        <DraftLeaveScope>
          {editor}
          {anotherDraft ? <DraftLeaveGuard dirty /> : null}
        </DraftLeaveScope>
      ) : (
        editor
      ),
    },
    { path: '/next', element: <h1>Saved model</h1> },
  ]);
  render(<RouterProvider router={router} />);
  return router;
}

it.each([true, false])(
  'allows a confirmed save to navigate after clearing its draft (scope=%s)',
  async (scoped) => {
    const router = setup('saved', scoped);
    try {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
      expect(await screen.findByRole('heading', { name: 'Saved model' })).toBeVisible();
      expect(router.state.location.search).toBe('?binding_key=11&donations_page=2');
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    } finally {
      router.dispose();
    }
  },
);

it.each(['failed', 'unknown'] as const)(
  'keeps an unsaved draft protected after a %s result',
  async (outcome) => {
    const router = setup(outcome);
    try {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
      expect(await screen.findByRole('alert')).toHaveTextContent(outcome);
      fireEvent.click(screen.getByRole('link', { name: 'Other page' }));
      expect(await screen.findByRole('alertdialog')).toBeVisible();
      fireEvent.click(screen.getByRole('button', { name: 'Stay here' }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
      expect(router.state.location.pathname).toBe('/');
      fireEvent.click(screen.getByRole('link', { name: 'Other page' }));
      fireEvent.click(await screen.findByRole('button', { name: 'Leave page' }));
      expect(await screen.findByRole('heading', { name: 'Saved model' })).toBeVisible();
    } finally {
      router.dispose();
    }
  },
);

it('keeps another editor protected when only one draft has been saved', async () => {
  const router = setup('saved', true, true);
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alertdialog')).toBeVisible();
    expect(router.state.location.pathname).toBe('/');
  } finally {
    router.dispose();
  }
});
