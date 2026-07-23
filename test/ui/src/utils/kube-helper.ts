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
  const dnsLabelRegex = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
  if (!dnsLabelRegex.test(normalized)) {
    throw new Error(
      `Invalid prefix "${prefix}": must be lowercase alphanumeric with hyphens, ` +
        `start and end with alphanumeric`
    );
  }
  // Limit prefix to 57 chars (suffix is 6 chars: "-" + 5-char random = 63 total)
  const maxPrefixLength = 57;
  if (normalized.length > maxPrefixLength) {
    throw new Error(
      `Prefix "${prefix}" too long (${normalized.length} chars). ` +
        `Max ${maxPrefixLength} chars to stay within Kubernetes 63-char limit.`
    );
  }
  // Generate 5-character random suffix
  const suffix = Math.random().toString(36).substring(2, 7);
  return `${normalized}-${suffix}`;
}
