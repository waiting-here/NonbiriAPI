import i18next from 'i18next';
import { expect, it, vi } from 'vitest';
import userEn from 'virtual:station-core/user/en';
import userZh from 'virtual:station-core/user/zh';
import adminEn from 'virtual:station-core/admin/en';
import adminZh from 'virtual:station-core/admin/zh';
import fullEn from '../../src/user/i18n/en.json';
import fullZh from '../../src/user/i18n/zh.json';
import adminFullEn from '../../src/admin/i18n/en.json';
import adminFullZh from '../../src/admin/i18n/zh.json';
import { loadFeature, observeFeature, prepareActiveFeatures } from '@shared/i18n/featureCatalog';
import { prepareLanguage } from '@shared/i18n';
import { privacyCopy } from '../../src/user/i18n/legalCopy';

it('keeps the original navigation and ordinary station copy, with long features outside both initial language packs', () => {
  for (const [core, full] of [
    [userEn, fullEn],
    [userZh, fullZh],
  ] as const) {
    const { privacy, terms, ...legal } = full.user.legal;
    expect(core).toEqual({
      ...full,
      user: {
        ...full.user,
        legal: { ...legal, privacy: { nav: privacy.nav }, terms: { nav: terms.nav } },
      },
    });
  }
  for (const [core, full] of [
    [adminEn, adminFullEn],
    [adminZh, adminFullZh],
  ] as const) {
    const restored = structuredClone(core) as typeof adminFullEn;
    expect(restored.admin.managementHistory).not.toHaveProperty('snapshot');
    restored.admin.managementHistory.snapshot = full.admin.managementHistory.snapshot;
    expect(restored).toEqual(full);
  }
});

it('shares a pending feature request, preserves both core and new copy, and loads only mounted features when changing language', async () => {
  const instance = i18next.createInstance();
  await instance.init({
    lng: 'en',
    fallbackLng: false,
    resources: {
      en: { translation: structuredClone(userEn) },
      zh: { translation: structuredClone(userZh) },
    },
  });
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  const fetcher = vi.fn(async (url: string) => {
    await waiting;
    return Response.json(
      url === privacyCopy.urls.en ? fullEn.user.legal.privacy : fullZh.user.legal.privacy,
    );
  });
  vi.stubGlobal('fetch', fetcher);
  const off = observeFeature(instance, privacyCopy);
  const second = observeFeature(instance, privacyCopy);
  const first = loadFeature(instance, privacyCopy, 'en');
  const again = loadFeature(instance, privacyCopy, 'en');
  expect(fetcher).toHaveBeenCalledTimes(1);
  release();
  await Promise.all([first, again]);
  expect(instance.t('user.legal.privacy.title')).toBe(fullEn.user.legal.privacy.title);
  expect(instance.t('user.legal.terms.nav')).toBe(fullEn.user.legal.terms.nav);
  off();
  await prepareLanguage(instance, 'zh');
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(instance.getResource('zh', 'translation', 'user.legal.privacy.title')).toBe(
    fullZh.user.legal.privacy.title,
  );
  second();
  instance.removeResourceBundle('zh', 'translation');
  await prepareActiveFeatures(instance, 'zh');
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it('retries failed or malformed responses without losing the current navigation labels', async () => {
  const instance = i18next.createInstance();
  await instance.init({
    lng: 'en',
    fallbackLng: false,
    resources: { en: { translation: structuredClone(userEn) } },
  });
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(new Response('', { status: 503 }))
    .mockResolvedValueOnce(Response.json({ unrelated: 'wrong shape' }))
    .mockResolvedValueOnce(Response.json(fullEn.user.legal.privacy));
  vi.stubGlobal('fetch', fetcher);
  await expect(loadFeature(instance, privacyCopy, 'en')).rejects.toThrow();
  await expect(loadFeature(instance, privacyCopy, 'en')).rejects.toThrow();
  expect(instance.getResource('en', 'translation', 'user.legal.privacy.nav')).toBe(
    fullEn.user.legal.privacy.nav,
  );
  await loadFeature(instance, privacyCopy, 'en');
  expect(instance.t('user.legal.privacy.title')).toBe(fullEn.user.legal.privacy.title);
  expect(fetcher).toHaveBeenCalledTimes(3);
});
