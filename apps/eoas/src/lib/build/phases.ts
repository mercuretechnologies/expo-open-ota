// Phase names and log markers follow the EAS build log format so the same
// grouped-log readers can display local builds.
export enum BuildPhase {
  BUILDER_INFO = 'BUILDER_INFO',
  CALCULATE_EXPO_UPDATES_RUNTIME_VERSION = 'CALCULATE_EXPO_UPDATES_RUNTIME_VERSION',
  CUSTOM = 'CUSTOM',
  EAGER_BUNDLE = 'EAGER_BUNDLE',
  GRADLE_BUILD_PROFILE = 'GRADLE_BUILD_PROFILE',
  PREBUILD = 'PREBUILD',
  PREPARE_ARTIFACTS = 'PREPARE_ARTIFACTS',
  PREPARE_CREDENTIALS = 'PREPARE_CREDENTIALS',
  PREPARE_PROJECT = 'PREPARE_PROJECT',
  READ_APP_CONFIG = 'READ_APP_CONFIG',
  RUN_EXPO_DOCTOR = 'RUN_EXPO_DOCTOR',
  RUN_GRADLEW = 'RUN_GRADLEW',
  SET_UP_BUILD_ENVIRONMENT = 'SET_UP_BUILD_ENVIRONMENT',
  UPLOAD_APPLICATION_ARCHIVE = 'UPLOAD_APPLICATION_ARCHIVE',
}

export enum BuildPhaseResult {
  SUCCESS = 'success',
  FAIL = 'failed',
  WARNING = 'warning',
  SKIPPED = 'skipped',
}

export enum LogMarker {
  START_PHASE = 'START_PHASE',
  END_PHASE = 'END_PHASE',
}

export const buildPhaseDisplayName: Record<BuildPhase, string> = {
  [BuildPhase.BUILDER_INFO]: 'Build machine info',
  [BuildPhase.CALCULATE_EXPO_UPDATES_RUNTIME_VERSION]: 'Calculate runtime version',
  [BuildPhase.CUSTOM]: 'Custom step',
  [BuildPhase.EAGER_BUNDLE]: 'Bundle JavaScript',
  [BuildPhase.GRADLE_BUILD_PROFILE]: 'Gradle build profile',
  [BuildPhase.PREBUILD]: 'Prebuild',
  [BuildPhase.PREPARE_ARTIFACTS]: 'Prepare artifacts',
  [BuildPhase.PREPARE_CREDENTIALS]: 'Prepare credentials',
  [BuildPhase.PREPARE_PROJECT]: 'Prepare project',
  [BuildPhase.READ_APP_CONFIG]: 'Read app config',
  [BuildPhase.RUN_EXPO_DOCTOR]: 'Run expo doctor',
  [BuildPhase.RUN_GRADLEW]: 'Run Gradle',
  [BuildPhase.SET_UP_BUILD_ENVIRONMENT]: 'Set up build environment',
  [BuildPhase.UPLOAD_APPLICATION_ARCHIVE]: 'Upload application archive',
};
