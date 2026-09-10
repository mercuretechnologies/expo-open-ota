import { AndroidConfig, IOSConfig } from '@expo/config-plugins';
import fs from 'fs-extra';
import path from 'path';

import { Client } from './vcs/vcs';

export enum Platform {
  ANDROID = 'android',
  IOS = 'ios',
}

export enum Workflow {
  GENERIC = 'generic',
  MANAGED = 'managed',
}

export async function resolveWorkflowAsync(
  projectDir: string,
  platform: Platform,
  vcsClient: Client
): Promise<Workflow> {
  let platformWorkflowMarkers: string[];
  try {
    platformWorkflowMarkers =
      platform === Platform.ANDROID
        ? [
            path.join(projectDir, 'android/app/build.gradle'),
            await AndroidConfig.Paths.getAndroidManifestAsync(projectDir),
          ]
        : [IOSConfig.Paths.getPBXProjectPath(projectDir)];
  } catch {
    return Workflow.MANAGED;
  }

  const vcsRootPath = path.normalize(await vcsClient.getRootPathAsync());
  for (const marker of platformWorkflowMarkers) {
    if (
      (await fs.pathExists(marker)) &&
      !(await vcsClient.isFileIgnoredAsync(path.relative(vcsRootPath, marker)))
    ) {
      return Workflow.GENERIC;
    }
  }
  return Workflow.MANAGED;
}

export async function resolveWorkflowPerPlatformAsync(
  projectDir: string,
  vcsClient: Client
): Promise<Record<Platform, Workflow>> {
  const [android, ios] = await Promise.all([
    resolveWorkflowAsync(projectDir, Platform.ANDROID, vcsClient),
    resolveWorkflowAsync(projectDir, Platform.IOS, vcsClient),
  ]);
  return { android, ios };
}
