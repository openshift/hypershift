/**
 * Constants for HyperShift UI tests.
 * Common PatternFly selectors (copied from console-e2e).
 */

// PatternFly loading indicators
export const PF_SPINNER = '.pf-v6-c-spinner';
export const PF_SKELETON = '.pf-v6-c-skeleton';

/**
 * Kubernetes DNS-1123 label validation.
 * RFC 1123 DNS label: [a-z0-9]([-a-z0-9]*[a-z0-9])?
 * - lowercase alphanumeric, hyphens allowed in the middle
 * - must start and end with alphanumeric
 * - max 63 characters
 */
export const DNS_1123_LABEL_REGEX = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const DNS_1123_MAX_LENGTH = 63;
