import { useQuery } from '@tanstack/react-query';
import { ApiError, apiFetch } from './http';
import { asRecord, type UnknownRecord } from './normalize';

// Display-only public site configuration. The backend /api/config endpoint
// projects a strict allowlist (site identity, donation guidance, legal
// overrides, and station state) and
// never carries secrets. The browser treats the response as a closed DTO:
// missing/unknown fields and wrong types fail closed instead of silently
// turning a malformed response into permissive maintenance=false state. The
// logo URL is additionally constrained to public HTTPS so a misconfigured or
// hostile value can never become a javascript:/data: vector when rendered
// into an <img src>. Credentials in a configured URL are also rejected
// because the browser must never send them to a branding host.

export interface PublicConfig {
  siteName: string;
  siteLogoURL: string;
  charityDonationNoticeZh: string;
  charityDonationNoticeEn: string;
  legalPrivacyOverrideZh: string;
  legalPrivacyOverrideEn: string;
  legalTermsOverrideZh: string;
  legalTermsOverrideEn: string;
  legalAuthoritativeLocale: 'zh' | 'en' | '';
  maintenanceMode: boolean;
  registrationOpen: boolean;
  announcementEpoch: string;
}

const MAX_LEGAL_OVERRIDE = 65536;
const MAX_DONATION_NOTICE = 8192;
const MAX_SITE_NAME = 256;
const MAX_LOGO_URL = 2048;
const PUBLIC_CONFIG_KEYS = new Set([
  'site_name',
  'site_logo_url',
  'charity_donation_notice_zh',
  'charity_donation_notice_en',
  'legal_privacy_override_zh',
  'legal_privacy_override_en',
  'legal_terms_override_zh',
  'legal_terms_override_en',
  'legal_authoritative_locale',
  'maintenance_mode',
  'registration_open',
  'announcement_epoch',
]);

// Every OID in the frozen wire contract uses 16 random bytes encoded as
// unpadded base64url. The final quantum has four legal tail characters when
// the unused bits are zero; keeping this check local prevents a malformed
// bootstrap from becoming a cache/event identity later in the app.
const ANNOUNCEMENT_EPOCH_PATTERN = /^b1e_[A-Za-z0-9_-]{21}[AQgw]$/;

function invalidPublicConfig(): never {
  throw new ApiError('invalid_response', 'The server returned an invalid public configuration.', 200);
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

function requiredText(record: UnknownRecord, key: string, maxBytes: number, multiline = false): string {
  const value = record[key];
  if (typeof value !== 'string' || byteLength(value) > maxBytes) invalidPublicConfig();
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    const allowedWhitespace = multiline && (codePoint === 9 || codePoint === 10 || codePoint === 13);
    if (codePoint < 32 || (codePoint >= 127 && codePoint <= 159)) {
      if (!allowedWhitespace) invalidPublicConfig();
    }
  }
  return value;
}

export function isPublicLogoURL(
  value: string | undefined | null,
  origin = typeof window !== 'undefined' ? window.location.origin : '',
): value is string {
  if (!value) return false;
  try {
    const url = new URL(value);
    return url.protocol === 'https:'
      && Boolean(url.hostname)
      && !url.username
      && !url.password
      && (!origin || url.origin !== origin);
  } catch {
    return false;
  }
}

// Multiline legal override text preserves newlines/tabs (operators author
// multi-paragraph documents) while dropping other control characters. This
// mirrors the backend multiline bound; the value is rendered as text nodes
// only, so it cannot become an HTML injection vector.
export function normalizePublicConfig(payload: unknown): PublicConfig {
  const record = asRecord(payload);
  if (!record || Object.keys(record).some((key) => !PUBLIC_CONFIG_KEYS.has(key))) invalidPublicConfig();
  for (const key of PUBLIC_CONFIG_KEYS) {
    if (!Object.prototype.hasOwnProperty.call(record, key)) invalidPublicConfig();
  }
  const rawLogo = requiredText(record, 'site_logo_url', MAX_LOGO_URL);
  const siteLogoURL = rawLogo && isPublicLogoURL(rawLogo) ? rawLogo : '';
  const legalAuthoritativeLocale = record.legal_authoritative_locale;
  if (legalAuthoritativeLocale !== '' && legalAuthoritativeLocale !== 'zh' && legalAuthoritativeLocale !== 'en') {
    invalidPublicConfig();
  }
  if (typeof record.maintenance_mode !== 'boolean' || typeof record.registration_open !== 'boolean') {
    invalidPublicConfig();
  }
  if (typeof record.announcement_epoch !== 'string' || !ANNOUNCEMENT_EPOCH_PATTERN.test(record.announcement_epoch)) {
    invalidPublicConfig();
  }
  return {
    siteName: requiredText(record, 'site_name', MAX_SITE_NAME),
    siteLogoURL,
    charityDonationNoticeZh: requiredText(
      record,
      'charity_donation_notice_zh',
      MAX_DONATION_NOTICE,
      true,
    ),
    charityDonationNoticeEn: requiredText(
      record,
      'charity_donation_notice_en',
      MAX_DONATION_NOTICE,
      true,
    ),
    legalPrivacyOverrideZh: requiredText(record, 'legal_privacy_override_zh', MAX_LEGAL_OVERRIDE, true),
    legalPrivacyOverrideEn: requiredText(record, 'legal_privacy_override_en', MAX_LEGAL_OVERRIDE, true),
    legalTermsOverrideZh: requiredText(record, 'legal_terms_override_zh', MAX_LEGAL_OVERRIDE, true),
    legalTermsOverrideEn: requiredText(record, 'legal_terms_override_en', MAX_LEGAL_OVERRIDE, true),
    legalAuthoritativeLocale: legalAuthoritativeLocale as 'zh' | 'en' | '',
    maintenanceMode: record.maintenance_mode,
    registrationOpen: record.registration_open,
    announcementEpoch: record.announcement_epoch,
  };
}

export function usePublicConfig(enabled = true, path = '/api/config') {
  return useQuery({
    queryKey: ['public-config', path] as const,
    queryFn: async () => {
      const payload = await apiFetch<unknown>(path);
      if (payload === undefined) throw new ApiError('invalid_response', 'The server returned an invalid response.', 200);
      return normalizePublicConfig(payload);
    },
    enabled,
    // Operator changes are infrequent and the response is no-store; a short
    // staleTime keeps a name/logo flip visible on reload without hammering.
    staleTime: 60_000,
    retry: 1,
  });
}
