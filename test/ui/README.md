# HyperShift UI E2E Tests

Playwright-based E2E test framework for HyperShift MCE console integration following console E2E patterns.

## Prerequisites

1. **Node.js** 20+
2. oc CLI installed and logged into your cluster
3. Playwright browsers installed
```bash
# Install dependencies and Playwright browsers
make test-ui-install
```

## Environment Variables

**Recommended:** Copy `.env.example` when making `.env` (gitignored). Playwright loads it via `src/config/index.ts`.

**Hub cluster + console authentication:**

| Variable           | Required | Default      | Description                                                         |
| ------------------ | -------- | ------------ | ------------------------------------------------------------------- |
| `HUB_URL`          | **Yes**  | -            | Hub cluster API URL (e.g., `https://api.cluster.com:6443`)          |
| `HUB_PASSWORD`     | **Yes**  | -            | Password for `oc login` and console UI authentication               |
| `CONSOLE_URL`      | No       | (derived)    | Console URL (derived from `HUB_URL` if not set)                     |
| `CONSOLE_USERNAME` | No       | `kubeadmin`  | Username for console login form                                     |
| `CONSOLE_IDP`      | No       | `kube:admin` | Identity provider link text on console login page                   |

**Test configuration (optional):**

| Variable    | Required | Default       | Description                                           |
| ----------- | -------- | ------------- | ----------------------------------------------------- |
| `CI`        | No       | `false`       | Set to `true` to enable CI mode (retries, forbidOnly) |
| `TEST_MODE` | No       | `integration` | Test mode (`integration`, `e2e`, etc.)                |

> **Note:** `CI` is parsed as a boolean. Only the string `"true"` enables CI mode; any other value (including `"false"` or unset) disables it.
>
> **Typical kubeadmin setup:** `.env` with just `HUB_URL` and `HUB_PASSWORD`.

### Example `.env`

```bash
HUB_URL=https://api.my-hub-cluster.example.com:6443
HUB_PASSWORD=my-kubeadmin-password
CONSOLE_USERNAME=kubeadmin
CONSOLE_IDP=kube:admin
```

## Playwright Projects

| Project  | Scope                                                                           | Test Path             |
| -------- | ------------------------------------------------------------------------------- | --------------------- |
| `setup`  | Authentication setup → saves session to `.auth/admin.json`                      | `src/auth.setup.ts`   |
| `common` | Platform-agnostic tests (smoke tests, infrastructure validation)                | `src/tests/common/**` |
| `agent`  | Agent-platform HyperShift tests (InfraEnv, discovery ISO, bare metal workflows) | `src/tests/agent/**`  |

Projects depend on `setup` for authentication and run with a saved console session.

**Note**: All tests migrated from stolostron/clc-ui-e2e (CNTRLPLANE-2104) are Agent platform tests. AWS and other platform tests are not in the initial migration scope.

## Running Tests
### All Tests
```bash
make test-ui
```
### Platform-Specific Tests
```bash
# Common tests (smoke tests, infrastructure)
make test-ui-common

# Agent platform tests
make test-ui-agent
```
### Development Mode
```bash
# Headed mode (see browser)
make test-ui-headed
```
### Direct Playwright CLI
```bash
cd test/ui

# Run all tests
npm test

# Run specific project
npm run test:common
npm run test:agent

# Run specific test file
npx playwright test src/tests/common/smoke.spec.ts

# UI mode (interactive)
npx playwright test --ui

# Debug mode (step through with inspector)
npx playwright test --debug
```

## Architecture
- **Playwright** for browser automation
- **TypeScript** with path aliases (`@config`, `@pages`, `@services`, etc.)
- **Page Object Model** pattern for UI interactions
- **Fixtures** for dependency injection (`oc` CLI service, unique name generator, cleanup tracker)

This directory maps to it as follows:
```text
test/ui/
├── src/
│   ├── auth.setup.ts              # OpenShift console authentication
│   ├── global-setup.ts            # Pre-test cleanup (.auth/ directory)
│   ├── config/                    # Environment configuration
│   ├── constants/                 # Selectors and constants
│   ├── services/                  # CLI services (OcCliService, etc.)
│   ├── pages/                     # Page Object Model classes
│   │   └── ...
│   ├── fixtures/                  # Playwright test fixtures (oc, uniqueName, cleanup)
│   │   └── hypershift-test.ts     # Base fixture extending Playwright test
│   ├── utils/                     # Helper utilities
│   └── tests/                     # Test specifications
│       ├── common/                # Platform-agnostic smoke tests
│       └── agent/                 # Agent platform tests
├── .env.example                   # Environment variable template
├── playwright.config.ts           # Playwright configuration
├── tsconfig.json                  # TypeScript configuration
└── package.json                   # Dependencies and scripts
```

## Writing New Tests
1. **Create a test file** in `src/tests/common/` or `src/tests/agent/`
2. **Use the HyperShift fixture:**
```typescript
import { test, expect } from '@fixtures/hypershift-test';

test.describe('My Feature', () => {
  test('should do something', async ({ page, oc, uniqueName, cleanup }) => {
    // page: Playwright Page object
    // oc: OcCliService for running oc commands
    // uniqueName: Unique test resource name (hypershift-ci-xxxxx)
    // cleanup: Automatic cleanup tracker for HostedClusters and namespaces
    // Your test logic here
  });
});
```
1. **Create page objects** in `src/pages/` following the Page Object Model pattern
2. **Add cleanup** for any resources created:
```typescript
cleanup.trackHostedCluster('my-cluster', 'clusters');
cleanup.trackNamespace('my-test-namespace');
```

