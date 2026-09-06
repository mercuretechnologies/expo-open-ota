export interface AndroidProfile {
  applicationId: string;
  mode: 'debug' | 'release';
  artifact: 'apk' | 'aab';
  developmentClient?: boolean;
}

export interface BuildProfile {
  channel?: string;
  environment?: string;
  android: AndroidProfile;
}

export interface XpremConfig {
  schemaVersion: 1;
  profiles: Record<string, BuildProfile>;
}
