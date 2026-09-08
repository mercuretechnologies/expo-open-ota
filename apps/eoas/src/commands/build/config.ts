import { Command, Flags } from '@oclif/core';
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
    const config = await readConfig(file).catch(error => {
      Log.error((error as Error).message);
      this.exit(2);
    });
    let output: unknown = config;
    if (flags.profile) {
      // Own-property check: the parsed object inherits Object.prototype, so a
      // name like "constructor" would otherwise resolve to a function.
      if (!Object.prototype.hasOwnProperty.call(config.profiles, flags.profile)) {
        Log.error(`Build profile "${flags.profile}" was not found in ${CONFIG_FILENAME}.`);
        this.exit(2);
      }
      output = config.profiles[flags.profile];
    }
    const serialized = JSON.stringify(output, null, 2);
    if (flags.json) {
      process.stdout.write(`${serialized}\n`);
    } else {
      Log.log(serialized);
    }
  }
}
