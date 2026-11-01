import type { ReactNode } from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Icon } from '@shared/components/Icon';
import { USER_SECTION_LINKS, type UserNavGroup } from '../navigation';
import './section-tabs.css';

/**
 * Pages of one top-level entry (for example services and models). Plain links keep each page
 * bookmarkable; the current page is marked with aria-current.
 */
const GROUP_LABEL_KEYS: Record<UserNavGroup, string> = {
  own: 'user.navigation.ownSections',
  records: 'user.records.title',
};

export function SectionTabs({
  group,
  className = '',
  badges = {},
}: {
  group: UserNavGroup;
  className?: string;
  /** Optional trailing content per link path, such as an open-issue count. */
  badges?: Partial<Record<string, ReactNode>>;
}) {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  return (
    <nav
      className={`nb-section-tabs${className ? ` ${className}` : ''}`}
      aria-label={t(GROUP_LABEL_KEYS[group])}
    >
      {USER_SECTION_LINKS[group].map((link) => {
        const current = pathname === link.to || pathname.startsWith(link.to + '/');
        return (
          <Link key={link.to} to={link.to} aria-current={current ? 'page' : undefined}>
            {link.icon ? <Icon name={link.icon} /> : null}
            <span>{t(link.labelKey)}</span>
            {badges[link.to] ?? null}
          </Link>
        );
      })}
    </nav>
  );
}
