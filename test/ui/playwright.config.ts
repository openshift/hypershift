import { defineConfig, devices } from '@playwright/test';
import { config } from './src/config/index';

/**
 * Playwright configuration for HyperShift UI E2E tests.
 */
export default defineConfig({
  testDir: './src/tests',
  /* Global setup - runs before all tests */
  globalSetup: require.resolve('./src/global-setup'),
  /* Timeouts - ACM/MCE console can be slow to load */
  timeout: 60000, // Per-test timeout (60s)
  expect: {
    timeout: 15000, // Per-assertion timeout (15s)
  },
  /* Run tests in files in parallel */
  fullyParallel: true,
  /* Fail the build on CI if you accidentally left test.only in the source code */
  forbidOnly: config.ci,
  /* Retry on CI only */
  retries: config.ci ? 2 : 0,
  /* Opt out of parallel tests on CI */
  workers: config.ci ? 1 : undefined,
  /* Reporters */
  reporter: [['html'], ['junit', { outputFile: 'test-results/junit.xml' }]],
  /* Shared settings for all projects */
  use: {
    /* Base URL for navigation */
    baseURL: config.consoleUrl,
    /* Collect trace when retrying failed tests */
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'on-first-retry',
    ignoreHTTPSErrors: true,
  },

  /* Test projects */
  projects: [
    /* Setup project - handles authentication */
    {
      name: 'setup',
      testDir: './src',
      testMatch: /auth\.setup\.ts/,
      timeout: 120_000,
    },
    /* Common - Platform-agnostic tests (smoke tests, infrastructure validation) */
    {
      name: 'common',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1920, height: 1080 },
        storageState: '.auth/admin.json',
      },
      dependencies: ['setup'],
      testMatch: /common\/.*\.spec\.ts/,
    },
    /* Agent Platform - HyperShift UI tests */
    {
      name: 'agent',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1920, height: 1080 },
        storageState: '.auth/admin.json',
      },
      dependencies: ['setup'],
      testMatch: /agent\/.*\.spec\.ts/,
    },
  ],
});
