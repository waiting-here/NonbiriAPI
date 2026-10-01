import { screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { PersonalAutomationGuide } from './PersonalAutomationGuide';
import { personalAutomationExamples as examples } from './personalAutomationExamples';

afterEach(() => vi.unstubAllGlobals());

describe('optional personal automation guide', () => {
  it.each(['en', 'zh'] as const)(
    'renders registered %s instructions and opens on demand without a request',
    async (locale) => {
      const fetchMock = vi.fn<typeof fetch>();
      vi.stubGlobal('fetch', fetchMock);
      const view = await renderWithProviders(<PersonalAutomationGuide />, {
        station: 'user',
        locale,
      });
      const summary = screen.getByText(
        locale === 'en' ? 'Manage your resources with scripts' : '用脚本管理自用资源',
      );
      const details = summary.closest('details')!;
      expect(details).not.toHaveAttribute('open');
      await view.user.click(summary.closest('summary')!);
      expect(details).toHaveAttribute('open');
      expect(screen.getByText('/api/automation/models/{id}/bindings')).toBeInTheDocument();
      expect(
        screen.getByText(
          locale === 'en' ? '4 · Check each result and retry' : '4 · 检查每项结果和重试',
        ),
      ).toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();
    },
  );

  it('provides bodyless Bearer reads and explicit JSON writes with distinct retained operation keys', () => {
    expect(examples.read).toContain('Authorization: Bearer $CALLER_KEY');
    expect(examples.read).not.toContain('Idempotency-Key');
    expect(examples.read).not.toContain('--data');
    expect(examples.importRequest).toContain('Content-Type: application/json');
    expect(examples.importRequest).toContain('Idempotency-Key: $IMPORT_OPERATION_KEY');
    expect(examples.importRequest).toContain('/api/automation/endpoints/42/keys/batch-import');
    expect(examples.bindRequest).toContain('Idempotency-Key: $BIND_OPERATION_KEY');
    expect(examples.bindRequest).toContain('/api/automation/models/23/bindings/batch');
    expect(JSON.parse(examples.importBody)).toMatchObject({
      ownership_confirmed: true,
      keys: [{ secret: 'fictional-provider-secret' }],
    });
    expect(JSON.parse(examples.bindBody)).toEqual({
      endpoint_key_ids: ['81', '82'],
      upstream_model_id: 'example/model',
      catalog_mode: 'manual',
    });
    expect(
      JSON.parse(examples.resultExample).results.map((row: { status: string }) => row.status),
    ).toEqual(['success', 'incomplete']);
  });
});
