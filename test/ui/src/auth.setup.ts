import { test as setup, expect } from '@playwright/test';
import path from 'path';
import { config } from './config';

/**
 * Authentication setup for Playwright tests.
 * Logs into the OpenShift console and saves authentication state to .auth/admin.json.
 *
 * This runs before all tests via the 'setup' project in playwright.config.ts.
 */
setup('authenticate as admin', async ({ page }) => {
  console.log('Authenticating to OpenShift console...');

  // Use shared config (consoleUrl is derived from HUB_URL or explicit CONSOLE_URL)
  const { consoleUrl, consoleUsername: username, consoleIdp: idp, hubPassword: password } = config;
  await page.goto(consoleUrl);
  await page.waitForLoadState('load');
  // Click the IDP link if present
  const idpLink = page.getByRole('link', { name: idp });
  const idpPresent = await idpLink.count();
  if (idpPresent > 0) {
    console.log('IDP link found, clicking...');
    await idpLink.click();
    await page.waitForLoadState('load');
  } else {
    console.log('IDP link not found, proceeding to username/password');
  }
  // Fill in credentials
  const usernameField = page.locator('input[name="username"], input[id="inputUsername"]').first();
  const passwordField = page
    .locator('input[name="password"], input[id="inputPassword"], input[type="password"]')
    .first();
  await usernameField.waitFor({ state: 'visible', timeout: 10000 });
  await usernameField.fill(username);
  console.log('Filled username field');
  await passwordField.fill(password);
  console.log('Filled password field');
  const loginButton = page.locator('button[type="submit"], button:has-text("Log in")').first();
  await loginButton.click();
  console.log('Clicked login button');

  // Wait for successful login (check for multiple possible indicators)
  await expect(
    page
      .locator('[data-test="user-dropdown"]')
      .or(page.getByRole('button', { name: 'Skip tour' }))
      .or(page.locator('button').filter({ hasText: username }))
      .or(page.locator('.co-username'))
  ).toBeVisible({ timeout: 30000 });
  console.log('Authentication successful');
  const authFile = path.join(__dirname, '../.auth/admin.json');
  await page.context().storageState({ path: authFile });
  console.log('Saved authentication state');
});
