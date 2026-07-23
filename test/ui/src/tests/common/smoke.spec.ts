import { test, expect } from '@fixtures/hypershift-test';
import { PF_SPINNER, PF_SKELETON } from '@constants';

/**
 * Smoke test to validate base infrastructure.
 * Verifies: auth, config, page objects, navigation.
 */
test.describe('HyperShift UI Infrastructure', () => {
  test('should load OpenShift console', async ({ page }) => {
    // Navigate to the console root
    await page.goto('/');
    await page.waitForLoadState('load');
    // Check for user dropdown (indicates successful login and page load)
    // Works on both traditional console and MCE fleet management pages
    const userButton = page.locator('[data-test="user-dropdown"]').or(page.locator('button').filter({ hasText: /kube.*admin/i }));
    await expect(userButton).toBeVisible({ timeout: 30000 });
    console.log('Successfully loaded OpenShift console');
  });

  test('should detect MCE console availability', async ({ page }) => {
    // Navigate to MCE clusters page
    await page.goto('/multicloud/infrastructure/clusters/managed');
    await page.waitForLoadState('load');
    const notFoundLocator = page.getByText(/404.*page not found/i);
    const spinnerLocator = page.locator(PF_SPINNER);
    try {
      // Wait for either 404 to appear OR spinners to disappear
      await Promise.race([
        notFoundLocator.waitFor({ state: 'visible', timeout: 5000 }),
        spinnerLocator.waitFor({ state: 'hidden', timeout: 5000 }),
      ]);
    } catch {
      // Catch if page loads immediately without spinners
    }
    const pageNotFound = (await notFoundLocator.count()) > 0;
    if (pageNotFound) {
      console.log('MCE not installed - expected for non-MCE clusters');
      return;
    }
    // Wait for PatternFly loading indicators to disappear
    await expect(page.locator(PF_SPINNER)).toHaveCount(0, { timeout: 30000 });
    await expect(page.locator(PF_SKELETON)).toHaveCount(0, { timeout: 30000 });
    await expect(page).toHaveURL(/\/multicloud\/infrastructure\/clusters\/managed/);
    console.log('MCE console available - page loaded successfully');
  });

  test('should generate unique names', async ({ uniqueName }) => {
    expect(uniqueName).toMatch(/^hypershift-ci-[a-z0-9]{5}$/);
    console.log(`Generated unique name: ${uniqueName}`);
  });

  test('should have oc CLI service available', async ({ oc }) => {
    const output = await oc.run(['version', '--client']);
    expect(output).toContain('Client Version');
    console.log('OcCliService is working');
  });
});
