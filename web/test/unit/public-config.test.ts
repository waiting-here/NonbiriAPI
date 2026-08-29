import { describe, expect, test } from 'vitest';
import { isPublicLogoURL, normalizePublicConfig } from '../../src/shared/query/publicConfig';

const VALID_CONFIG = {
  site_name: 'NonbiriAPI',
  site_logo_url: '',
  legal_privacy_override_zh: '',
  legal_privacy_override_en: '',
  legal_terms_override_zh: '',
  legal_terms_override_en: '',
  legal_authoritative_locale: '',
  maintenance_mode: false,
  registration_open: true,
  announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
} as const;

describe('public bootstrap contract', () => {
  test('normalizes the complete closed DTO, including the announcement epoch', () => {
    expect(normalizePublicConfig(VALID_CONFIG)).toEqual({
      siteName: 'NonbiriAPI',
      siteLogoURL: '',
      legalPrivacyOverrideZh: '',
      legalPrivacyOverrideEn: '',
      legalTermsOverrideZh: '',
      legalTermsOverrideEn: '',
      legalAuthoritativeLocale: '',
      maintenanceMode: false,
      registrationOpen: true,
      announcementEpoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
    });
  });

  test.each([
    ['null', null],
    ['array', []],
    ['missing field', { ...VALID_CONFIG, registration_open: undefined }],
    ['wrong boolean type', { ...VALID_CONFIG, maintenance_mode: 'false' }],
    ['unknown field', { ...VALID_CONFIG, default_locale: 'en' }],
    ['invalid authoritative locale', { ...VALID_CONFIG, legal_authoritative_locale: 'fr' }],
    ['invalid announcement prefix', { ...VALID_CONFIG, announcement_epoch: 'ann_AAAAAAAAAAAAAAAAAAAAAA' }],
    ['invalid announcement length', { ...VALID_CONFIG, announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAA' }],
    ['invalid announcement tail bits', { ...VALID_CONFIG, announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAB' }],
  ])('rejects malformed bootstrap: %s', (_label, payload) => {
    expect(() => normalizePublicConfig(payload)).toThrow(/invalid public configuration/i);
  });

  test('rejects control characters and overlong bounded text', () => {
    expect(() => normalizePublicConfig({ ...VALID_CONFIG, site_name: 'bad\u0001name' })).toThrow();
    expect(() => normalizePublicConfig({ ...VALID_CONFIG, legal_terms_override_en: 'x'.repeat(65537) })).toThrow();
  });
});

describe('public logo URL policy', () => {
  test('rejects same-origin branding so it cannot carry site credentials', () => {
    expect(isPublicLogoURL('https://app.example/logo.svg', 'https://app.example')).toBe(false);
    expect(isPublicLogoURL('https://cdn.example/logo.svg', 'https://app.example')).toBe(true);
  });

  test('requires cross-origin HTTPS without URL credentials', () => {
    expect(isPublicLogoURL('http://cdn.example/logo.svg', 'https://app.example')).toBe(false);
    expect(isPublicLogoURL('https://user:pass@cdn.example/logo.svg', 'https://app.example')).toBe(false);
    expect(isPublicLogoURL('https://cdn.example/logo.svg', 'https://app.example')).toBe(true);
  });
});
