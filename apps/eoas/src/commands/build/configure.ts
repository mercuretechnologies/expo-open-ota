import { Command } from '@oclif/core';
import path from 'path';

import { CONFIG_FILENAME, writeNewConfig } from '../../lib/buildConfig/config';
import { prepareBuildConfig } from '../../lib/buildConfig/setup';
import { getPrivateExpoConfigAsync } from '../../lib/expoConfig';
import Log from '../../lib/log';
import { isExpoInstalled } from '../../lib/package';

export default class BuildConfigure extends Command {
  static override description = 'Configure Android build profiles in xprem.json';
  static override examples = ['<%= config.bin %> build:configure'];

  public async run(): Promise<void> {
    const projectDir = process.cwd();
    if (!isExpoInstalled(projectDir)) {
      Log.error('Expo is not installed in this project. Please install Expo first.');
      this.exit(2);
    }
    const expoConfig = await getPrivateExpoConfigAsync(projectDir);
    if (!expoConfig) {
      Log.error('Could not find an Expo config in this project.');
      this.exit(2);
    }
    Log.warn(
      'xprem builds require control-plane mode backed by PostgreSQL; stateless mode does not support builds.\n' +
        'Learn more: https://mercure-technologies.gitbook.io/xprem/stateless-mode/overview'
    );

    const target = path.join(projectDir, CONFIG_FILENAME);
    const prepared = await prepareBuildConfig(projectDir, expoConfig.android?.package);
    if (prepared.kind === 'cancelled') {
      Log.cancel('Build configuration cancelled. No files were changed.');
    } else if (prepared.kind === 'write') {
      await writeNewConfig(target, prepared.config);
      Log.succeed(`Created ${CONFIG_FILENAME}.`);
    }
  }
}
