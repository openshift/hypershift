const { execFileSync } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const repoRoot = path.resolve(__dirname, '../..');

module.exports = async function extensionHook(hookName, context) {
  if (hookName === 'beforeEach') {
    const patchPath = context.test?.vars?.patchFile;
    if (patchPath) {
      const fullPath = path.resolve(__dirname, patchPath);
      if (fs.existsSync(fullPath)) {
        const worktreeName = `eval-${Date.now()}-${crypto.randomUUID()}`;
        const worktreeDir = path.join(require('os').tmpdir(), 'hypershift-eval', worktreeName);
        let worktreeCreated = false;
        try {
          execFileSync('git', ['worktree', 'add', worktreeDir, 'HEAD'], { cwd: repoRoot, stdio: 'pipe' });
          worktreeCreated = true;
          execFileSync('git', ['apply', fullPath], { cwd: worktreeDir, stdio: 'pipe' });
          console.log(`Created worktree and applied patch: ${worktreeDir}`);
          context.test.vars.worktreePath = worktreeDir;
        } catch (e) {
          if (worktreeCreated) {
            try {
              execFileSync('git', ['worktree', 'remove', worktreeDir, '--force'], { cwd: repoRoot, stdio: 'pipe' });
            } catch (_) {}
          }
          console.error(`Failed to create worktree or apply patch: ${e.message}`);
        }
      }
    }
    return context;
  }

  if (hookName === 'afterEach') {
    const worktreeDir = context.test?.vars?.worktreePath;
    if (worktreeDir) {
      try {
        execFileSync('git', ['worktree', 'remove', worktreeDir, '--force'], { cwd: repoRoot, stdio: 'pipe' });
        console.log(`Removed worktree: ${worktreeDir}`);
      } catch (e) {
        console.error(`Failed to remove worktree: ${e.message}`);
      }
    }
  }
};
