import { execFile } from 'child_process';
import { promisify } from 'util';
import * as path from 'path';

const execFilePromise = promisify(execFile);

/**
 * Validate a Kubernetes namespace name.
 * Must be DNS-1123 label: lowercase alphanumeric, hyphens, max 63 chars.
 */
function validateNamespace(namespace: string): void {
  if (!namespace || typeof namespace !== 'string') {
    throw new Error('Namespace must be a non-empty string');
  }
  // RFC 1123 DNS label: [a-z0-9]([-a-z0-9]*[a-z0-9])?
  const dnsLabelRegex = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
  if (!dnsLabelRegex.test(namespace)) {
    throw new Error(
      `Invalid namespace name: "${namespace}". Must be lowercase alphanumeric with hyphens, max 63 chars.`
    );
  }
}

/**
 * Validate a YAML file path.
 * Must be a relative or absolute path with .yaml or .yml extension.
 * Rejects path traversal attempts.
 */
function validateYamlPath(yamlPath: string): void {
  if (!yamlPath || typeof yamlPath !== 'string') {
    throw new Error('YAML path must be a non-empty string');
  }
  const ext = path.extname(yamlPath).toLowerCase();
  if (ext !== '.yaml' && ext !== '.yml') {
    throw new Error(`Invalid YAML path: "${yamlPath}". Must end with .yaml or .yml`);
  }
  // Reject path traversal attempts - check for ".." segments BEFORE normalization
  const segments = yamlPath.split(/[\\/]/); // Split on both forward and backslash
  if (segments.includes('..')) {
    throw new Error(`Path traversal detected in YAML path: "${yamlPath}"`);
  }
}

/**
 * Service for executing OpenShift CLI (oc) commands.
 * Security: Uses execFile with argument arrays to prevent shell injection.
 */
export class OcCliService {
  /**
   * Execute oc command with arguments (no shell interpolation).
   * @param args - Command arguments as array (e.g., ['get', 'pods'])
   * @param timeoutMs - Timeout in milliseconds (default: 90s, buffer above typical --timeout=60s operations)
   */
  async run(args: string[], timeoutMs = 90000): Promise<string> {
    try {
      const { stdout } = await execFilePromise('oc', args, { timeout: timeoutMs });
      return stdout.trim();
    } catch (error) {
      // Timeout failures
      if (error && typeof error === 'object' && 'killed' in error && error.killed) {
        throw new Error(`oc command timed out after ${timeoutMs}ms`);
      }
      console.error('oc command failed. Command details redacted for security.');
      throw error;
    }
  }

  async applyYaml(yamlPath: string): Promise<string> {
    validateYamlPath(yamlPath);
    return this.run(['apply', '-f', yamlPath]);
  }

  async deleteYaml(yamlPath: string): Promise<string> {
    validateYamlPath(yamlPath);
    return this.run(['delete', '-f', yamlPath, '--ignore-not-found']);
  }

  async getConsoleUrl(): Promise<string> {
    const host = await this.run([
      'get',
      'route',
      'console',
      '-n',
      'openshift-console',
      '-o',
      'jsonpath={.spec.host}',
    ]);
    if (!host || host.trim() === '') {
      throw new Error(
        'Console route not found. Ensure the console route exists in openshift-console namespace.'
      );
    }
    return `https://${host}`;
  }

  async getCurrentUser(): Promise<string> {
    const output = await this.run(['whoami']);
    return output.trim();
  }

  async deleteNamespace(namespace: string): Promise<void> {
    validateNamespace(namespace);
    await this.run(['delete', 'namespace', namespace, '--ignore-not-found', '--wait=true'], 300000);
  }
}
