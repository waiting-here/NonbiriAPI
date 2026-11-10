import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ProtocolRerankField } from './ProtocolRerankField';

describe('service rerank settings', () => {
  it('previews the standard suffix and a same-origin absolute custom path without changing the format', async () => {
    function Form() {
      const [path, setPath] = useState('');
      return (
        <ProtocolRerankField
          formats={['generic', 'cohere']}
          value="cohere"
          onChange={vi.fn()}
          path={path}
          onPathChange={setPath}
          baseURL="https://api.example/compatible/v1/"
        />
      );
    }
    const view = await renderWithProviders(<Form />, { station: 'user' });
    await view.user.click(screen.getByText('Rerank settings'));
    expect(screen.getByText('https://api.example/compatible/v1/rerank')).toBeVisible();
    const path = screen.getByRole('textbox', { name: /Rerank interface path/ });
    await view.user.type(path, '/compatible-api/v1/reranks');
    expect(screen.getByText('https://api.example/compatible-api/v1/reranks')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Rerank format' })).toHaveValue('cohere');
    await view.user.clear(path);
    expect(screen.getByText('https://api.example/compatible/v1/rerank')).toBeVisible();
    await view.user.type(path, '//other.example/reranks');
    expect(screen.queryByText('https://other.example/reranks')).not.toBeInTheDocument();
  });

  it('shows configured paths on opening and respects a non-configurable service', async () => {
    const view = await renderWithProviders(
      <ProtocolRerankField
        formats={['generic']}
        value="generic"
        onChange={vi.fn()}
        path="/saved/reranks"
        onPathChange={vi.fn()}
        baseURL="https://api.example/v1"
      />,
      { station: 'user' },
    );
    expect(screen.getByRole('textbox', { name: /Rerank interface path/ })).toBeVisible();
    view.rerender(
      <ProtocolRerankField
        formats={['generic']}
        value="generic"
        onChange={vi.fn()}
        onPathChange={vi.fn()}
        pathConfigurable={false}
      />,
    );
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Rerank format' })).toBeVisible();
  });

  it('keeps template format controls unchanged without a path editor', async () => {
    const view = await renderWithProviders(
      <ProtocolRerankField formats={['generic']} value="generic" onChange={vi.fn()} />,
      { station: 'admin' },
    );
    await view.user.click(screen.getByText('Rerank format', { selector: 'strong' }));
    expect(screen.getByRole('combobox', { name: 'Rerank format' })).toBeVisible();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('offers the registered dedicated HTTP rerank path without a format choice', async () => {
    const view = await renderWithProviders(
      <ProtocolRerankField
        formats={['cohere']}
        value="cohere"
        onChange={vi.fn()}
        path=""
        onPathChange={vi.fn()}
        baseURL="https://api.example/v2"
      />,
      { station: 'user' },
    );
    await view.user.click(screen.getByText('Rerank settings'));
    expect(screen.getByRole('textbox', { name: /Rerank interface path/ })).toBeVisible();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(screen.getByText('https://api.example/v2/rerank')).toBeVisible();
  });
});
