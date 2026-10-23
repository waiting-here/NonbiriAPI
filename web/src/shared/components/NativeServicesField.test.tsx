import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { NativeServicesField, type NativeServicesFieldProps } from './NativeServicesField';

const environment = { station: 'user', role: 'user', locale: 'en' } as const;

describe('native service selection', () => {
  it('changes Chat and Responses independently while preserving other selections', async () => {
    const onChange = vi.fn();
    const props: NativeServicesFieldProps = {
      options: ['gateway_text', 'responses', 'chat_completions'],
      value: ['responses', 'systemone'],
      onChange,
    };
    const view = await renderWithProviders(<NativeServicesField {...props} />, environment);
    const chat = screen.getByRole('checkbox', { name: 'Chat completions' });
    expect(chat).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Responses' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Gateway text generation' })).not.toBeChecked();

    await view.user.click(chat);
    expect(onChange).toHaveBeenLastCalledWith(['chat_completions', 'responses', 'systemone']);
    expect(chat).not.toBeChecked();
    view.rerender(
      <NativeServicesField {...props} value={['chat_completions', 'responses', 'systemone']} />,
    );
    await view.user.click(screen.getByRole('checkbox', { name: 'Responses' }));
    expect(onChange).toHaveBeenLastCalledWith(['chat_completions', 'systemone']);
    expect(screen.getByRole('checkbox', { name: 'Gateway text generation' })).not.toBeChecked();
  });

  it('leaves a single service unselected until the user selects it by keyboard', async () => {
    const onChange = vi.fn();
    const props: NativeServicesFieldProps = {
      options: ['anthropic_messages'],
      value: [],
      onChange,
    };
    const view = await renderWithProviders(<NativeServicesField {...props} />, environment);
    const messages = screen.getByRole('checkbox', { name: 'Anthropic Messages' });
    expect(messages).toBeVisible();
    expect(messages).not.toBeChecked();
    expect(screen.getByText('Awaiting selection')).toBeVisible();
    expect(onChange).not.toHaveBeenCalled();
    await view.user.tab();
    expect(messages).toHaveFocus();
    await view.user.keyboard('[Space]');
    expect(onChange).toHaveBeenLastCalledWith(['anthropic_messages']);
    view.rerender(<NativeServicesField {...props} value={['anthropic_messages']} />);
    expect(messages).toBeChecked();
    expect(screen.queryByText('Awaiting selection')).not.toBeInTheDocument();
  });

  it('shows rerank formats only when selected and supplied without choosing or clearing one', async () => {
    const onFormatChange = vi.fn();
    const props: NativeServicesFieldProps = {
      options: ['rerank'],
      value: ['rerank'],
      onChange: vi.fn(),
      onRerankFormatChange: onFormatChange,
    };
    const view = await renderWithProviders(<NativeServicesField {...props} />, environment);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    view.rerender(<NativeServicesField {...props} rerankFormats={[]} />);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();

    const withFormats: NativeServicesFieldProps = {
      ...props,
      rerankFormats: ['generic', 'cohere'],
      rerankFormat: null,
    };
    view.rerender(<NativeServicesField {...withFormats} />);
    const format = screen.getByRole('combobox', { name: 'Rerank format' });
    expect(format).toHaveValue('');
    expect(onFormatChange).not.toHaveBeenCalled();
    await view.user.selectOptions(format, 'cohere');
    expect(onFormatChange).toHaveBeenLastCalledWith('cohere');
    view.rerender(<NativeServicesField {...withFormats} rerankFormat="cohere" />);
    await view.user.selectOptions(format, '');
    expect(onFormatChange).toHaveBeenLastCalledWith(null);
    const changes = onFormatChange.mock.calls.length;
    view.rerender(<NativeServicesField {...withFormats} value={[]} rerankFormat="cohere" />);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    view.rerender(<NativeServicesField {...withFormats} rerankFormat="cohere" />);
    expect(screen.getByRole('combobox', { name: 'Rerank format' })).toHaveValue('cohere');
    expect(onFormatChange).toHaveBeenCalledTimes(changes);
  });

  it('keeps selected services and formats visible without callbacks when read-only or disabled', async () => {
    const onChange = vi.fn();
    const onFormatChange = vi.fn();
    const props: NativeServicesFieldProps = {
      options: ['chat_completions', 'rerank'],
      value: ['chat_completions', 'rerank'],
      onChange,
      rerankFormats: ['generic', 'cohere'],
      rerankFormat: 'cohere',
      onRerankFormatChange: onFormatChange,
      readOnly: true,
    };
    const view = await renderWithProviders(<NativeServicesField {...props} />, environment);
    const chat = screen.getByRole('checkbox', { name: 'Chat completions' });
    const format = screen.getByRole('combobox', { name: 'Rerank format' });
    expect(chat).toBeChecked();
    expect(chat).toBeDisabled();
    expect(format).toHaveValue('cohere');
    expect(format).toBeDisabled();
    await view.user.click(chat);
    await view.user.selectOptions(format, 'generic');
    expect(onChange).not.toHaveBeenCalled();
    expect(onFormatChange).not.toHaveBeenCalled();

    view.rerender(<NativeServicesField {...props} readOnly={false} disabled />);
    expect(chat).toBeDisabled();
    expect(format).toBeDisabled();
    await view.user.click(chat);
    await view.user.selectOptions(format, 'generic');
    expect(onChange).not.toHaveBeenCalled();
    expect(onFormatChange).not.toHaveBeenCalled();
  });
});
