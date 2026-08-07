import { DNS_1123_LABEL_REGEX, DNS_1123_MAX_LENGTH } from '@constants';

/**
 * Helper utilities for Kubernetes resource management.
 * Pure functions only - no Playwright or external dependencies.
 */

/**
 * Generate a safe Kubernetes resource name with random suffix.
 * @param prefix - Name prefix (e.g., 'ci', 'test', 'hypershift-ci')
 * @returns Name like 'hypershift-ci-abc12'
 * @throws Error if prefix is invalid or too long
 */
export function generateSafeName(prefix: string): string {
  if (!prefix || typeof prefix !== 'string') {
    throw new Error('Prefix must be a non-empty string');
  }
  const normalized = prefix.toLowerCase().trim();
  // Validate DNS-1123 label format
  if (!DNS_1123_LABEL_REGEX.test(normalized)) {
    throw new Error(
      `Invalid prefix "${prefix}": must be lowercase alphanumeric with hyphens, ` +
        `start and end with alphanumeric`
    );
  }
  // Limit prefix to 57 chars (suffix is 6 chars: "-" + 5-char random = 63 total)
  const maxPrefixLength = DNS_1123_MAX_LENGTH - 6; // Reserve 6 chars for "-" + 5-char suffix
  if (normalized.length > maxPrefixLength) {
    throw new Error(
      `Prefix "${prefix}" too long (${normalized.length} chars). ` +
        `Max ${maxPrefixLength} chars to stay within Kubernetes ${DNS_1123_MAX_LENGTH}-char limit.`
    );
  }
  // Generate 5-character random suffix using crypto.randomUUID() (consistent with k8s naming)
  const suffix = crypto.randomUUID().slice(0, 5);
  return `${normalized}-${suffix}`;
}
