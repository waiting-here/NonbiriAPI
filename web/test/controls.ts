import { expect, type Locator, type Page } from '@playwright/test';

export async function setControlChecked(control: Locator, checked: boolean) {
  if ((await control.isChecked()) !== checked) {
    await expect(control).toBeEnabled();
    await control.focus();
    await control.press('Space');
  }
  await expect(control).toBeChecked({ checked });
}

export async function submitPendingReview(
  page: Page,
  label: string,
  trigger = page.getByRole('button', { name: label, exact: true }),
) {
  await trigger.click();
  const confirmation = page.getByRole('alertdialog', { name: label, exact: true });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole('button', { name: label, exact: true }).click();
}
