import { test as base, expect } from '@playwright/test';
import { OcCliService } from '@services';
import { generateSafeName } from '@utils';

/**
 * Cleanup tracker for test resources.
 * Automatically deletes HostedClusters and namespaces created during tests.
 */
type CleanupTracker = {
  trackHostedCluster: (name: string, namespace: string) => void;
  trackNamespace: (namespace: string) => void;
};

/**
 * HyperShift test fixtures.
 * Extends Playwright test with HyperShift-specific fixtures.
 */
type HyperShiftFixtures = {
  oc: OcCliService;
  uniqueName: string;
  cleanup: CleanupTracker;
  // TODO: Add HyperShift-specific page objects here
};

export const test = base.extend<HyperShiftFixtures>({
  oc: async ({}, use) => {
    await use(new OcCliService());
  },
  uniqueName: async ({}, use) => {
    await use(generateSafeName('hypershift-ci'));
  },

  /**
   * Cleanup tracker fixture.
   * Tracks HostedClusters and namespaces created during tests and deletes them after test completion.
   */
  cleanup: async ({ oc }, use, testInfo) => {
    const hostedClusters: Array<{ name: string; namespace: string }> = [];
    const namespaces: string[] = [];
    const tracker: CleanupTracker = {
      trackHostedCluster: (name, namespace) => {
        console.log(`[cleanup] Tracking HostedCluster: ${namespace}/${name}`);
        hostedClusters.push({ name, namespace });
      },
      trackNamespace: (namespace) => {
        console.log(`[cleanup] Tracking namespace: ${namespace}`);
        namespaces.push(namespace);
      },
    };
    await use(tracker);
    // Cleanup after test completes
    if (hostedClusters.length > 0 || namespaces.length > 0) {
      console.log(`[cleanup] Test "${testInfo.title}" finished, cleaning up resources...`);
      for (const { name, namespace } of hostedClusters) {
        try {
          console.log(`[cleanup] Deleting HostedCluster ${namespace}/${name}...`);
          await oc.run(['delete', 'hostedcluster', name, '-n', namespace, '--ignore-not-found=true']);
        } catch (error) {
          console.error(`[cleanup] Failed to delete HostedCluster ${namespace}/${name}:`, error);
        }
      }
      // Wait for HostedClusters to finish deleting
      if (hostedClusters.length > 0) {
        console.log('[cleanup] Waiting for HostedCluster deletions to complete...');
        for (const { name, namespace } of hostedClusters) {
          try {
            // Poll until the resource is gone
            const maxAttempts = 30;
            for (let i = 0; i < maxAttempts; i++) {
              const result = await oc.run([
                'get',
                'hostedcluster',
                name,
                '-n',
                namespace,
                '--ignore-not-found=true',
              ]);
              if (!result || result.trim() === '') {
                console.log(`[cleanup] HostedCluster ${namespace}/${name} deleted`);
                break;
              }
              if (i < maxAttempts - 1) {
                await new Promise((resolve) => setTimeout(resolve, 2000));
              }
            }
          } catch (error) {
            console.error(`[cleanup] Error waiting for HostedCluster ${namespace}/${name}:`, error);
          }
        }
      }
      // Delete namespaces
      for (const namespace of namespaces) {
        try {
          console.log(`[cleanup] Deleting namespace ${namespace}...`);
          // Pass 75s execFile timeout (longer than oc's --timeout=60s to avoid race)
          await oc.run(['delete', 'namespace', namespace, '--ignore-not-found=true', '--timeout=60s'], 75000);
        } catch (error) {
          console.error(`[cleanup] Failed to delete namespace ${namespace}:`, error);
        }
      }
      console.log(`[cleanup] Cleanup complete for test "${testInfo.title}"`);
    }
  },
});

export { expect };
