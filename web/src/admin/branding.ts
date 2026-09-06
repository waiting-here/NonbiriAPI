import { useQuery } from '@tanstack/react-query';
import { apiFetch } from '@shared/query/http';
import { isPublicLogoURL } from '@shared/query/publicConfig';
import { record, string } from '@shared/operations/wire';

export function useAdminBranding() {
  return useQuery({
    queryKey: ['admin-public-branding'],
    queryFn: async () => {
      const value = record(
        await apiFetch<unknown>('/admin/api/branding'),
        ['site_name', 'site_logo_url'],
        'public branding',
      );
      const siteName = string(value.site_name, 'site name', { max: 256, bytes: 256 });
      const logo = string(value.site_logo_url, 'site logo', { max: 2048, bytes: 2048 });
      return { siteName, siteLogoURL: isPublicLogoURL(logo) ? logo : '' };
    },
    staleTime: 60_000,
    retry: false,
  });
}
