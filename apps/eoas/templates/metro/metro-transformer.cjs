const fs = require('node:fs/promises');
const path = require('node:path');

const settings = require('./.eoas-metro-check.json');
const upstream = require(settings.upstream);
const { checkEnvironment } = require(settings.checker);

module.exports = {
  ...upstream,
  async transform(args) {
    const isSource = /\.[cm]?[jt]sx?$/.test(args.filename);
    const isDependency = args.filename.split(/[/\\]/).includes('node_modules');
    if (isSource && !isDependency) {
      try {
        checkEnvironment(args.src, path.basename(args.filename), settings.known);
      } catch (error) {
        await fs.appendFile(settings.report, `${error.message}\n`);
        throw new Error('EOAS environment check failed');
      }
    }
    return upstream.transform(args);
  },
};
