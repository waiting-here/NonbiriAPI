import { useQuery } from '@tanstack/react-query';
import { ApiError, apiFetch } from './http';
import { asRecord, optionalText, text, type UnknownRecord } from './normalize';

// Display-only public site configuration. The backend /api/config endpoint
// projects a strict allowlist (site_name, site_logo_url, default_locale) and
// never carries secrets. Values are bounded and control-character-cleaned
// again here; the logo URL is additionally constrained to http(s) so a
// misconfigured or hostile value can never become a javascript:/data: vector
// when rendered into an <img src>.

export interface PublicConfig {
  siteName: string;
  siteLogoURL: string;
  defaultLocale: 'zh' | 'en' | '';
  legalPrivacyOverrideZh: string;
  legalPrivacyOverrideEn: string;
  legalTermsOverrideZh: string;
  legalTermsOverrideEn: string;
  legalAuthoritativeLocale: 'zh' | 'en' | '';
}

const HTTP_S = /^https?:\/\//i;
const MAX_LEGAL_OVERRIDE = 65536;

// Multiline legal override text preserves newlines/tabs (operators author
// multi-paragraph documents) while dropping other control characters. This
// mirrors the backend multiline bound; the value is rendered as text nodes
// only, so it cannot become an HTML injection vector.
function multilineText(value: unknown, max: number): string {
  if (typeof value !== 'string') return '';
  let out = '';
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint === 9 || codePoint === 10 || codePoint === 13) {
      out += character;
      continue;
    }
    if (codePoint < 32 || codePoint === 127) continue;
    out += character;
  }
  const characters = Array.from(out);
  return characters.length > max ? characters.slice(0, max).join('') : out;
}

function normalizePublicConfig(payload: unknown): PublicConfig {
  const record = (asRecord(payload) ?? {}) as UnknownRecord;
  const rawLogo = optionalText(record.site_logo_url, 2048);
  const siteLogoURL = rawLogo && HTTP_S.test(rawLogo) ? rawLogo : '';
  return {
    siteName: text(record.site_name, 256, ''),
    siteLogoURL,
    defaultLocale:
      record.default_locale === 'zh' || record.default_locale === 'en'
        ? (record.default_locale as 'zh' | 'en')
        : '',
    legalPrivacyOverrideZh: multilineText(record.legal_privacy_override_zh, MAX_LEGAL_OVERRIDE),
    legalPrivacyOverrideEn: multilineText(record.legal_privacy_override_en, MAX_LEGAL_OVERRIDE),
    legalTermsOverrideZh: multilineText(record.legal_terms_override_zh, MAX_LEGAL_OVERRIDE),
    legalTermsOverrideEn: multilineText(record.legal_terms_override_en, MAX_LEGAL_OVERRIDE),
    legalAuthoritativeLocale:
      record.legal_authoritative_locale === 'zh' || record.legal_authoritative_locale === 'en'
        ? (record.legal_authoritative_locale as 'zh' | 'en')
        : '',
  };
}

export function usePublicConfig() {
  return useQuery({
    queryKey: ['public-config'] as const,
    queryFn: async () => {
      const payload = await apiFetch<unknown>('/api/config');
      if (payload === undefined) throw new ApiError('invalid_response', 'The server returned an invalid response.', 200);
      return normalizePublicConfig(payload);
    },
    // Operator changes are infrequent and the response is no-store; a short
    // staleTime keeps a name/logo flip visible on reload without hammering.
    staleTime: 60_000,
    retry: 1,
  });
}
