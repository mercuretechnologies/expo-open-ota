const fs = require('node:fs/promises');
const path = require('node:path');

module.exports = (async () => {
  const settingsPath = path.join(__dirname, '.eoas-metro-check.json');
  const settings = JSON.parse(await fs.readFile(settingsPath, 'utf8'));
  const config = settings.original
    ? await require('metro-config').loadConfig({ cwd: __dirname, config: settings.original })
    : require('expo/metro-config').getDefaultConfig(__dirname);

  // Preserve the project's transformer, including any custom Babel behavior.
  settings.upstream = config.transformer.babelTransformerPath;
  await fs.writeFile(settingsPath, JSON.stringify(settings));
  config.transformer.babelTransformerPath = path.join(__dirname, 'eoas-metro-transformer.cjs');
  config.resetCache = true;
  return config;
})();
