import { Command, Flags } from '@oclif/core';

import { buildAndroid } from '../../lib/build/android';
import Log from '../../lib/log';

export default class Build extends Command {
  static override description =
    'Build a signed Android APK/AAB locally using remote xprem credentials.\n\nEnvironment checking inspects app.config source (not its imported Node helpers) and application modules visited by Metro before Expo inlining (including workspace modules, excluding node_modules). Static dot/bracket accesses and destructuring are checked; dynamic or aliased process.env access requires --ignore-env-check. Dead branches within visited modules may still require keys. Only direct process.env.EXPO_PUBLIC_X accesses are inlined by Expo (bracket/destructured reads are only checked for presence); other supplied variables remain config/tooling inputs. Debug APKs normally load JavaScript from Metro; their graph is validated with export --dev. Maintained Android projects retain their native Expo settings. Product flavors and split artifacts are not supported.';
  static override flags = {
    profile: Flags.string({ char: 'e', required: true, description: 'Profile in xprem.json' }),
    channel: Flags.string({ description: 'Override the profile channel/environment selection' }),
    'env-file': Flags.string({ description: 'Dotenv file overriding individual server variables' }),
    'ignore-env-check': Flags.boolean({
      description: 'Bypass static environment checks (including unverifiable dynamic access)',
      default: false,
    }),
    serverUrl: Flags.string({ description: 'Override updates.url for the build server' }),
    appId: Flags.string({ description: 'Override expo-app-id for bootstrap config resolution' }),
    output: Flags.string({
      description:
        'Destination APK/AAB file (must not already exist; defaults to a unique name in build-artifacts)',
    }),
    packageRunner: Flags.string({ description: 'Package runner used for Expo commands' }),
    'java-home': Flags.string({
      description: 'JDK directory exported as JAVA_HOME for Gradle',
    }),
    'android-sdk': Flags.string({
      description: 'Local Android SDK directory (overrides ANDROID_HOME and sdk.dir)',
    }),
    verbose: Flags.boolean({
      description:
        'Print all build output instead of a compact preview (full logs are always saved)',
      default: false,
    }),
  };
  static override examples = [
    '<%= config.bin %> build --profile production --channel production --env-file .env.build',
  ];
  public async run(): Promise<void> {
    const { flags } = await this.parse(Build);
    try {
      await buildAndroid(process.cwd(), {
        profile: flags.profile,
        channel: flags.channel,
        envFile: flags['env-file'],
        ignoreEnvCheck: flags['ignore-env-check'],
        serverUrl: flags.serverUrl,
        appId: flags.appId,
        output: flags.output,
        packageRunner: flags.packageRunner,
        verbose: flags.verbose,
        javaHome: flags['java-home'],
        androidSdk: flags['android-sdk'],
      });
    } catch (error) {
      Log.error(error instanceof Error ? error.message : 'Build failed.');
      this.exit(1);
    }
  }
}
