const fs = require('node:fs/promises');
const { createRequire } = require('node:module');
const path = require('node:path');

async function main() {
  const [project, fingerprintModule, output] = process.argv.slice(2);
  const fingerprint = await require(fingerprintModule).createFingerprintAsync(project, {
    platforms: ['android'],
    silent: true,
    ignorePaths: ['build-artifacts/**/*', '.eoas-export/**/*', 'android/local.properties'],
  });
  const projectRequire = createRequire(path.join(project, 'package.json'));
  const expoSdk = projectRequire('expo/package.json').version;
  await fs.writeFile(output, JSON.stringify({ fingerprint: fingerprint.hash, expoSdk }), {
    mode: 0o600,
  });
}

main().catch(() => {
  process.stderr.write(
    'Could not compute the Expo fingerprint. Check the project configuration and installed dependencies.\n'
  );
  process.exitCode = 1;
});
