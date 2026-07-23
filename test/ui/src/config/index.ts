import * as dotenv from 'dotenv';
import * as path from 'path';

dotenv.config({ path: path.join(__dirname, '../../.env') });
export function getEnvVar(name: string, required = true): string {
  const value = process.env[name];
  if (!value && required) {
    throw new Error(`Required environment variable ${name} is not set. Check your .env file.`);
  }
  return value || '';
}

/**
 * Derive console URL from hub API URL.
 * Converts https://api.cluster.com:6443 -> https://console-openshift-console.apps.cluster.com
 *
 * Security: Validates HUB_URL at trust boundary using allow-list approach.
 */
function deriveConsoleUrl(hubUrl: string): string {
  const explicitConsoleUrl = getEnvVar('CONSOLE_URL', false);
  if (explicitConsoleUrl) {
    // Validate CONSOLE_URL
    let parsedConsoleUrl: URL;
    try {
      parsedConsoleUrl = new URL(explicitConsoleUrl);
    } catch {
      throw new Error('CONSOLE_URL must be a valid URL. Please check your .env file.');
    }
    if (parsedConsoleUrl.protocol !== 'https:') {
      throw new Error('CONSOLE_URL must use HTTPS protocol');
    }
    if (parsedConsoleUrl.username || parsedConsoleUrl.password) {
      throw new Error('CONSOLE_URL must not contain credentials (username/password)');
    }
    if (parsedConsoleUrl.search || parsedConsoleUrl.hash) {
      throw new Error('CONSOLE_URL must not contain query parameters or fragments');
    }

    // Console URL should point to a console hostname (not api.)
    const consoleHostname = parsedConsoleUrl.hostname.toLowerCase();
    if (consoleHostname.startsWith('api.')) {
      throw new Error(
        'CONSOLE_URL should point to the console (e.g., console-openshift-console.apps...), not the API server'
      );
    }
    return explicitConsoleUrl;
  }

  // Parse and validate HUB_URL at trust boundary
  let parsedUrl: URL;
  try {
    parsedUrl = new URL(hubUrl);
  } catch {
    throw new Error('HUB_URL must be a valid URL. Please check your .env file.');
  }

  // Security validations (allow-list approach)
  if (parsedUrl.protocol !== 'https:') {
    throw new Error('HUB_URL must use HTTPS protocol');
  }
  if (parsedUrl.username || parsedUrl.password) {
    throw new Error('HUB_URL must not contain credentials (username/password)');
  }
  if (parsedUrl.pathname !== '/' && parsedUrl.pathname !== '') {
    throw new Error('HUB_URL must not contain a path');
  }
  if (parsedUrl.search || parsedUrl.hash) {
    throw new Error('HUB_URL must not contain query parameters or fragments');
  }

  // Hostname must start with "api." (case-insensitive, normalized)
  const hostname = parsedUrl.hostname.toLowerCase();
  if (!hostname.startsWith('api.')) {
    throw new Error('HUB_URL hostname must start with "api." (e.g., api.cluster.example.com)');
  }

  // Extract cluster domain (everything after "api.")
  const clusterDomain = hostname.slice(4); // Remove "api." prefix
  if (!clusterDomain || clusterDomain.length === 0) {
    throw new Error('HUB_URL must have a cluster domain after "api." prefix');
  }

  // Construct console URL from validated domain
  return `https://console-openshift-console.apps.${clusterDomain}`;
}

/**
 * Environment configuration for HyperShift UI E2E tests.
 */
export const config = {
  // Hub cluster
  hubUrl: getEnvVar('HUB_URL'),
  hubPassword: getEnvVar('HUB_PASSWORD'),
  // Console URL (derived from HUB_URL or explicit CONSOLE_URL)
  consoleUrl: deriveConsoleUrl(getEnvVar('HUB_URL')),
  // Console authentication
  consoleUsername: getEnvVar('CONSOLE_USERNAME', false) || 'kubeadmin',
  consoleIdp: getEnvVar('CONSOLE_IDP', false) || 'kube:admin',
  // Test configuration
  ci: getEnvVar('CI', false) === 'true',
  testMode: getEnvVar('TEST_MODE', false) || 'integration',
};

export default config;
