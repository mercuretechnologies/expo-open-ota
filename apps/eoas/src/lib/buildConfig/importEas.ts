import { resolveEasProfile, validateEasConfig } from './eas';
import { ProfileNameSchema, ResourceNameSchema } from './schema';
import { BuildProfile, XpremConfig } from './types';

export type ImportIssue = { level: 'warning' | 'error'; path: string; message: string };
export type EasImport = { config: XpremConfig; issues: ImportIssue[] };

export function convertEasConfig(input: unknown, applicationId = ''): EasImport {
  const root = validateEasConfig(input);
  const build = root.build;
  const issues: ImportIssue[] = [];
  const profiles: XpremConfig['profiles'] = Object.create(null);

  function omitted(location: string): void {
    issues.push({
      level: 'warning',
      path: location,
      message: 'Not imported. Review this option before using the xprem profile.',
    });
  }

  for (const key of Object.keys(root)) {
    if (key !== 'build' && key !== '$schema') {
      omitted(key);
    }
  }
  for (const name of Object.keys(build)) {
    const rawSource = build[name];
    const source = resolveEasProfile(build, name);
    const location = `build.${name}`;
    const android = source.android ?? {};
    const effective = { ...source, ...android };
    if (ProfileNameSchema.validate(name).error) {
      issues.push({
        level: 'error',
        path: location,
        message: 'The EAS profile name is not a valid xprem profile name.',
      });
    }
    for (const key of Object.keys(rawSource)) {
      if (!['extends', 'android', 'channel', 'developmentClient', 'distribution'].includes(key)) {
        omitted(`${location}.${key}`);
      }
    }
    for (const key of Object.keys(rawSource.android ?? {})) {
      if (
        !['buildType', 'gradleCommand', 'channel', 'developmentClient', 'distribution'].includes(
          key
        )
      ) {
        omitted(`${location}.android.${key}`);
      }
    }
    const developmentClient = effective.developmentClient === true;
    let mode: BuildProfile['android']['mode'] = developmentClient ? 'debug' : 'release';
    let artifact: BuildProfile['android']['artifact'] =
      developmentClient ||
      android.buildType === 'apk' ||
      (android.buildType === undefined && effective.distribution === 'internal')
        ? 'apk'
        : 'aab';
    if (android.gradleCommand !== undefined) {
      const tasks: Record<
        string,
        { mode: BuildProfile['android']['mode']; artifact: BuildProfile['android']['artifact'] }
      > = {
        ':app:assembleDebug': { mode: 'debug', artifact: 'apk' },
        ':app:assembleRelease': { mode: 'release', artifact: 'apk' },
        ':app:bundleRelease': { mode: 'release', artifact: 'aab' },
      };
      const task =
        typeof android.gradleCommand === 'string' &&
        Object.prototype.hasOwnProperty.call(tasks, android.gradleCommand)
          ? tasks[android.gradleCommand]
          : undefined;
      if (!task) {
        issues.push({
          level: 'error',
          path: `${location}.android.gradleCommand`,
          message:
            'Custom Gradle tasks cannot be converted automatically. Configure this profile manually.',
        });
      } else {
        ({ mode, artifact } = task);
      }
    }
    if (developmentClient && mode !== 'debug') {
      issues.push({
        level: 'error',
        path: `${location}.developmentClient`,
        message: 'A development client with a release Gradle task has no v1 equivalent.',
      });
    }
    const profile: BuildProfile = {
      android: {
        applicationId,
        mode,
        artifact,
        ...(developmentClient ? { developmentClient: true } : {}),
      },
    };
    if (effective.channel !== undefined) {
      if (ResourceNameSchema.validate(effective.channel).error) {
        issues.push({
          level: 'error',
          path: `${location}.channel`,
          message: 'The EAS channel is not a valid xprem resource name.',
        });
      } else if (developmentClient) {
        issues.push({
          level: 'warning',
          path: `${location}.channel`,
          message:
            'Not imported: EAS development clients do not pin an update channel. Select a xprem channel or environment explicitly if needed.',
        });
      } else {
        profile.channel = effective.channel;
      }
    }
    profiles[name] = profile;
  }
  return { config: { schemaVersion: 1, profiles }, issues };
}
