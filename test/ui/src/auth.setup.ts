import { test as setup, expect } from '@playwright/test';
import path from 'path';
import { chmod } from 'fs/promises';
import { config } from './config';

/**
 * Authentication setup for Playwright tests.
 * Logs into the OpenShift console and saves authentication state to .auth/admin.json.
 */
setup('authenticate as admin', async ({ page }, testInfo) => {
  await setup.step('Navigate to console', async () => {
    const { consoleUrl } = config;
    testInfo.annotations.push({ type: 'console_url', description: consoleUrl });
    await page.goto(consoleUrl);
    await page.waitForLoadState('load');
  });

  await setup.step('Select IDP if present', async () => {
    const { consoleIdp: idp } = config;
    const idpLink = page.getByRole('link', { name: idp });
    const idpPresent = await idpLink.count();
    if (idpPresent > 0) {
      await idpLink.click();
      await page.waitForLoadState('load');
    }
  });

  await setup.step('Enter credentials and login', async () => {
    const { consoleUsername: username, hubPassword: password } = config;
    const usernameField = page.locator('input[name="username"], input[id="inputUsername"]').first();
    const passwordField = page
      .locator('input[name="password"], input[id="inputPassword"], input[type="password"]')
      .first();
    await usernameField.waitFor({ state: 'visible', timeout: 10000 });
    await usernameField.fill(username);
    await passwordField.fill(password);
    const loginButton = page.locator('button[type="submit"], button:has-text("Log in")').first();
    await loginButton.click();
  });

  await setup.step('Verify authentication success', async () => {
    const { consoleUsername: username } = config;
    await expect(
      page
        .locator('[data-test="user-dropdown"]')
        .or(page.getByRole('button', { name: 'Skip tour' }))
        .or(page.locator('button').filter({ hasText: username }))
        .or(page.locator('.co-username'))
    ).toBeVisible({ timeout: 30000 });
  });

  await setup.step('Save authentication state', async () => {
    const authFile = path.join(__dirname, '../.auth/admin.json');
    await page.context().storageState({ path: authFile });
    // Security: Restrict permissions to owner-only (prevents session token theft)
    await chmod(authFile, 0o600);
    testInfo.annotations.push({ type: 'auth_file', description: authFile });
  });
});
