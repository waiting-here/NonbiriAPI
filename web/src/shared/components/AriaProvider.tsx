import type { ReactNode } from 'react';
import { I18nProvider, RouterProvider } from 'react-aria-components';
import { useTranslation } from 'react-i18next';

export function AriaProvider({
  children,
  navigate,
}: {
  children: ReactNode;
  navigate: (href: string) => void;
}) {
  const { i18n } = useTranslation();
  return (
    <I18nProvider locale={i18n.resolvedLanguage?.startsWith('en') ? 'en-US' : 'zh-CN'}>
      <RouterProvider navigate={navigate}>{children}</RouterProvider>
    </I18nProvider>
  );
}
