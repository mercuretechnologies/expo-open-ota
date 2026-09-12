import { Args, Command } from '@oclif/core';
import path from 'path';

import { uploadBuildArtifact } from '../../lib/build/artifacts';
import { withBuildLog } from '../../lib/build/log';
import Log from '../../lib/log';

export default class BuildUpload extends Command {
  static override description =
    'Retry uploading a local APK/AAB using its adjacent .build.json metadata, without rebuilding or reserving another build number.';
  static override args = {
    artifact: Args.string({ required: true, description: 'Local APK/AAB path' }),
  };
  async run(): Promise<void> {
    const { args } = await this.parse(BuildUpload);
    try {
      const id = await withBuildLog(
        process.cwd(),
        'upload',
        'Artifact upload',
        async log => await uploadBuildArtifact(path.resolve(args.artifact), log)
      );
      Log.succeed(`Build ${id} is available in the dashboard.`);
    } catch (error) {
      Log.error(error instanceof Error ? error.message : 'Artifact upload failed.');
      this.exit(1);
    }
  }
}
