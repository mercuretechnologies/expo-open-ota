import fs from 'fs-extra';
import { createRequire } from 'module';
import os from 'os';
import path from 'path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { installMetroCheck } from '../workspace';

// The transformer loads the compiled checker from dist/, so `npm run build` must have run.
describe('Metro environment check', () => {
  let project: string;
  let temporary: string;
  beforeEach(async () => {
    project = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-metro-project-'));
    temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'eoas-metro-temporary-'));
  });
  afterEach(async () => {
    await fs.remove(project);
    await fs.remove(temporary);
  });

  it.each([false, true])(
    'wraps the project transformer and reports unknown variables (existing config: %s)',
    async existingConfig => {
      const upstream = path.join(project, 'upstream.cjs');
      await fs.writeFile(
        upstream,
        'module.exports = { transform: async args => args, getCacheKey: () => "upstream" };'
      );
      const config = { transformer: { babelTransformerPath: upstream }, customOption: 'retained' };
      if (existingConfig) {
        await fs.writeFile(
          path.join(project, 'metro.config.cjs'),
          `module.exports = Promise.resolve(${JSON.stringify(config)});`
        );
        await fs.outputFile(
          path.join(project, 'node_modules/metro-config/index.js'),
          'exports.loadConfig = async ({config}) => require(config);'
        );
      } else {
        await fs.outputFile(
          path.join(project, 'node_modules/expo/metro-config.js'),
          `exports.getDefaultConfig = () => (${JSON.stringify(config)});`
        );
      }

      const report = await installMetroCheck(project, temporary, ['EXPO_PUBLIC_NAV']);

      const nodeRequire = createRequire(path.join(project, 'package.json'));
      const metro = await nodeRequire('./metro.config.cjs');
      expect(metro.customOption).toBe('retained');
      expect(metro.resetCache).toBe(true);
      const transformer = nodeRequire(metro.transformer.babelTransformerPath);
      expect(transformer.getCacheKey()).toBe('upstream');

      const known = {
        src: 'process.env.EXPO_PUBLIC_NAV',
        filename: path.join(project, 'index.ts'),
      };
      expect(await transformer.transform(known)).toEqual(known);
      await expect(
        transformer.transform({ ...known, src: 'process.env.UNKNOWN_KEY' })
      ).rejects.toThrow('EOAS environment check failed');
      expect(await fs.readFile(report, 'utf8')).toContain('UNKNOWN_KEY');

      const dependency = {
        src: 'process.env.UNKNOWN_KEY',
        filename: path.join(project, 'node_modules/dependency/index.js'),
      };
      expect(await transformer.transform(dependency)).toEqual(dependency);
    }
  );
});
