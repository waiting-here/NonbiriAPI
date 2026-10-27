import { expect, type Locator, type Page } from '@playwright/test';
import en from '../src/user/i18n/en.json' with { type: 'json' };
import zh from '../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../src/shared/i18n/common/zh.json' with { type: 'json' };

export type ServiceLocale = 'en' | 'zh';
export interface BrowserService {
  endpoint: {
    id: string;
    note: string;
    base_url: string;
    connection_id: string;
    configuration_type: string;
    connector_type: string;
    native_services: string[];
  };
  key: {
    id: string;
    note: string;
    display_head: string;
    display_tail: string;
    credential_id: string;
    credential_version: string;
    discovery_source_id: string | null;
    revision: string;
    force_store_false: boolean;
    native_services: string[];
  };
}
export function serviceCopy(locale: ServiceLocale = 'en') {
  return (locale === 'zh' ? zh : en).user.workflows;
}
export function servicePanel(page: Page, locale: ServiceLocale = 'en', addingEntry = false) {
  const copy = (locale === 'zh' ? commonZh : commonEn).common.connections;
  return page.getByRole('region', {
    name: addingEntry ? copy.addEntry : serviceCopy(locale).addService,
    exact: true,
  });
}
export async function fillService(
  panel: Locator,
  input: {
    name: string;
    url: string;
    protocol?: string;
    secret?: string;
    discovery?: boolean;
    locale?: ServiceLocale;
  },
) {
  const locale = input.locale ?? 'en';
  const copy = (locale === 'zh' ? commonZh : commonEn).common.connections;
  const core = (locale === 'zh' ? zh : en).user.core;
  const services = (locale === 'zh' ? zh : en).user.services;
  const open = async (title: string) => {
    const fold = panel
      .locator('summary')
      .filter({ has: panel.page().getByText(title, { exact: true }) })
      .locator('..');
    if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
    return fold;
  };
  const protocols: Record<string, string> = {
    'openai-compatible': core['connector.openai'],
    'anthropic-compatible': core['connector.anthropic'],
    'ai-sdk-gateway-v4': core['connector.gatewayV4'],
  };
  const label = protocols[input.protocol ?? 'openai-compatible'];
  expect(label).toBeTruthy();
  await panel.getByLabel(services.serviceAddress, { exact: true }).fill(input.url);
  await open(serviceCopy(locale).serviceName);
  await panel.getByLabel(serviceCopy(locale).serviceName, { exact: true }).fill(input.name);
  if (input.protocol !== undefined || input.discovery === false) {
    await open(services.advancedSetup);
    await panel
      .getByRole('combobox', { name: copy.protocol, exact: true })
      .selectOption({ label: label! });
  }
  if (input.discovery === false) {
    await open(copy.discoveryOptional);
    await panel
      .getByRole('checkbox', { name: serviceCopy(locale).discoverModels, exact: true })
      .uncheck();
  }
  if (input.secret !== undefined) {
    const newKey = panel.getByRole('radio', { name: copy.newCredential, exact: true });
    if (await newKey.count()) await newKey.locator('..').click();
    await panel.getByLabel(copy.secret, { exact: true }).fill(input.secret);
  } else {
    await panel
      .getByRole('radio', { name: copy.existingCredential, exact: true })
      .locator('..')
      .click();
    const choices = panel.getByRole('table', { name: copy.chooseCredential, exact: true });
    await expect(choices.getByRole('button', { name: copy.select, exact: true })).toHaveCount(1);
    await choices.getByRole('button', { name: copy.select, exact: true }).click();
  }
}
export async function submitService(
  panel: Locator,
  locale: ServiceLocale = 'en',
  recoverLost = false,
) {
  const copy = serviceCopy(locale);
  const services = (locale === 'zh' ? zh : en).user.services;
  const check = panel.getByRole('button', {
    name: (locale === 'zh' ? commonZh : commonEn).common.connections.checkResult,
    exact: true,
  });
  const submit = panel
    .getByRole('button', { name: services.addAndFetch, exact: true })
    .or(panel.getByRole('button', { name: copy.continueService, exact: true }));
  for (let attempt = 0; attempt < (recoverLost ? 6 : 1); attempt++) {
    if (await panel.getByText(copy.serviceReady, { exact: true }).isVisible()) break;
    await expect(submit).toBeEnabled();
    await submit.click();
    if (recoverLost)
      await expect
        .poll(async () => {
          if ((await check.isVisible()) && (await check.isEnabled())) await check.click();
          return (
            (await panel.getByText(copy.serviceReady, { exact: true }).isVisible()) ||
            (await submit.evaluateAll((buttons) =>
              buttons.some((button) => !(button as HTMLButtonElement).disabled),
            ))
          );
        })
        .toBe(true)
        .catch(async (error: unknown) => {
          throw new Error('Service recovery did not finish: ' + (await panel.innerText()), {
            cause: error,
          });
        });
  }
  await expect(panel.getByText(copy.serviceReady, { exact: true })).toBeVisible();
}
export async function readService(page: Page, name: string): Promise<BrowserService> {
  const origin = new URL(page.url()).origin;
  const response = await page.request.get(
    origin + '/api/endpoints?page=1&page_size=100&q=' + encodeURIComponent(name),
  );
  expect(response.status()).toBe(200);
  const endpoints = ((await response.json()).data as BrowserService['endpoint'][]).filter(
    (row) => row.note === name,
  );
  expect(endpoints).toHaveLength(1);
  const endpoint = endpoints[0]!;
  expect(endpoint.connection_id).toBeTruthy();
  const keys = await page.request.get(
    origin + '/api/endpoints/' + endpoint.id + '/keys?page=1&page_size=100',
  );
  expect(keys.status()).toBe(200);
  const rows = (await keys.json()).data as BrowserService['key'][];
  expect(rows).toHaveLength(1);
  return { endpoint, key: rows[0]! };
}
export async function protocolServices(page: Page, protocol: string) {
  const response = await page.request.get(
    new URL(page.url()).origin + '/api/endpoint-create-options',
  );
  expect(response.status()).toBe(200);
  const options = (await response.json()).native_service_options as {
    connector_type: string;
    native_services: string[];
  }[];
  const configured = options.find((row) => row.connector_type === protocol);
  expect(configured).toBeTruthy();
  return configured!.native_services;
}
