import { expect, type Locator } from '@playwright/test';

export async function setControlChecked(control: Locator, checked: boolean) {
  if ((await control.isChecked()) !== checked) {
    await expect(control).toBeEnabled();
    await control.focus();
    await control.press('Space');
  }
  await expect(control).toBeChecked({ checked });
}
