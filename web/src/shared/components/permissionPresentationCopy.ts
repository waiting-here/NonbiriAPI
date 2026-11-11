import type { TFunction } from 'i18next';

export function permissionPresentationCopy(t: TFunction): {
  modes: Record<string, string>;
  pairs: Record<string, string>;
  groups: Record<string, { title: string; help: string }>;
} {
  return {
    modes: {
      off: t('management.permissions.modes.off'),
      read: t('management.permissions.modes.read'),
      manage: t('management.permissions.modes.manage'),
      custom: t('management.permissions.modes.custom'),
    },
    pairs: {
      callerKeys: t('management.permissions.pairs.callerKeys'),
      blacklist: t('management.permissions.pairs.blacklist'),
      endpoints: t('management.permissions.pairs.endpoints'),
      priceTemplates: t('management.permissions.pairs.priceTemplates'),
      risk: t('management.permissions.pairs.risk'),
      announcements: t('management.permissions.pairs.announcements'),
      maintenance: t('management.permissions.pairs.maintenance'),
    },
    groups: {
      users: {
        title: t('management.permissions.groups.users.title'),
        help: t('management.permissions.groups.users.help'),
      },
      economy: {
        title: t('management.permissions.groups.economy.title'),
        help: t('management.permissions.groups.economy.help'),
      },
      safety: {
        title: t('management.permissions.groups.safety.title'),
        help: t('management.permissions.groups.safety.help'),
      },
      donations: {
        title: t('management.permissions.groups.donations.title'),
        help: t('management.permissions.groups.donations.help'),
      },
      models: {
        title: t('management.permissions.groups.models.title'),
        help: t('management.permissions.groups.models.help'),
      },
      audit: {
        title: t('management.permissions.groups.audit.title'),
        help: t('management.permissions.groups.audit.help'),
      },
      site: {
        title: t('management.permissions.groups.site.title'),
        help: t('management.permissions.groups.site.help'),
      },
      assistant: {
        title: t('management.permissions.groups.assistant.title'),
        help: t('management.permissions.groups.assistant.help'),
      },
    },
  };
}
