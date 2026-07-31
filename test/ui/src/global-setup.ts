import * as fs from 'fs-extra';
import * as path from 'path';

/**
 * Global setup runs before all tests.
 * Cleans up .auth/ directory to ensure fresh authentication.
 */
async function globalSetup() {
  const authDir = path.join(__dirname, '../.auth');
  if (fs.existsSync(authDir)) {
    console.log('Cleaning up .auth directory...');
    await fs.remove(authDir);
  }
  await fs.ensureDir(authDir);
  console.log('Created fresh .auth directory');
}

export default globalSetup;
