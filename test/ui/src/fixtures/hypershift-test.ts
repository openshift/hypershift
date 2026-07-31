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
  cleanup: async ({ oc, uniqueName }, use, testInfo) => {
    const hostedClusters: Array<{ name: string; namespace: string }> = [];
    const namespaces: string[] = [];
    const tracker: CleanupTracker = {
      trackHostedCluster: (name, namespace) => {
        testInfo.annotations.push({
          type: 'tracked_hostedcluster',
          description: `${namespace}/${name}`,
        });
        hostedClusters.push({ name, namespace });
      },
      trackNamespace: (namespace) => {
        testInfo.annotations.push({
          type: 'tracked_namespace',
          description: namespace,
        });
        namespaces.push(namespace);
      },
    };
    await use(tracker);
    // Cleanup after test completes
    if (hostedClusters.length > 0 || namespaces.length > 0) {
      const prefix = uniqueName.substring(0, uniqueName.lastIndexOf('-'));
      testInfo.annotations.push({
        type: 'cleanup_prefix',
        description: prefix,
      });
      for (const { name, namespace } of hostedClusters) {
        // Only delete resources created by this test run
        if (!name.startsWith(prefix)) {
          testInfo.annotations.push({
            type: 'cleanup_skipped',
            description: `HostedCluster ${namespace}/${name} - does not match prefix "${prefix}"`,
          });
          continue;
        }
        try {
          await base.step(`Delete HostedCluster ${namespace}/${name} (wait up to 10m)`, async () => {
            await oc.deleteHostedCluster(name, namespace);
          });
        } catch (error) {
          testInfo.annotations.push({
            type: 'cleanup_error',
            description: `Failed to delete HostedCluster ${namespace}/${name}: ${error}`,
          });
        }
      }
      for (const namespace of namespaces) {
        // Only delete namespaces created by this test run
        if (!namespace.startsWith(prefix)) {
          testInfo.annotations.push({
            type: 'cleanup_skipped',
            description: `namespace ${namespace} - does not match prefix "${prefix}"`,
          });
          continue;
        }
        try {
          await base.step(`Delete namespace ${namespace}`, async () => {
            await oc.deleteNamespace(namespace);
          });
        } catch (error) {
          testInfo.annotations.push({
            type: 'cleanup_error',
            description: `Failed to delete namespace ${namespace}: ${error}`,
          });
        }
      }
    }
  },
});

export { expect };
