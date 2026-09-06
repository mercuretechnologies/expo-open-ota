import { Command, Flags } from '@oclif/core';
import fs from 'fs-extra';
import path from 'path';

import { CONFIG_FILENAME, readConfig } from '../../lib/buildConfig/config';
import Log from '../../lib/log';

export default class BuildConfig extends Command {
  static override description = 'Validate and display the local xprem build configuration';
  static override flags = {
    json: Flags.boolean({ description: 'Output JSON only' }),
    profile: Flags.string({ char: 'e', description: 'Display a single build profile' }),
  };
  static override examples = [
    '<%= config.bin %> build:config',
    '<%= config.bin %> build:config --profile production',
    '<%= config.bin %> build:config --profile production --json',
  ];

  public async run(): Promise<void> {
    const { flags } = await this.parse(BuildConfig);
    const file = path.resolve(CONFIG_FILENAME);
    if (!(await fs.pathExists(file))) {
      Log.error(`${CONFIG_FILENAME} was not found. Run "eoas build:configure" to create it.`);
      this.exit(2);
    }
    const config = await readConfig(file).catch(error => {
      Log.error(`Invalid ${CONFIG_FILENAME}: ${(error as Error).message}`);
      this.exit(2);
    });
    const output = flags.profile ? config.profiles[flags.profile] : config;
    if (!output) {
      Log.error(`Build profile "${flags.profile}" was not found in ${CONFIG_FILENAME}.`);
      this.exit(2);
    }
    const serialized = JSON.stringify(output, null, 2);
    if (flags.json) {
      process.stdout.write(`${serialized}\n`);
    } else {
      Log.log(serialized);
    }
  }
}
