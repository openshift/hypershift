import { test, expect } from '@fixtures/hypershift-test';

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
    const pageNotFound = (await notFoundLocator.count()) > 0;
    if (pageNotFound) {
      console.log('MCE not installed - expected for non-MCE clusters');
      return;
    }
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
