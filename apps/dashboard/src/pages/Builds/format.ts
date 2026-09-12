import { BuildRecord, BuildStatus } from '@/lib/api';

export const formatBytes = (bytes: number) => {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${units[unit]}`;
};

export const formatDuration = (durationMs: number) => {
  if (!Number.isFinite(durationMs) || durationMs < 0) return '—';
  const totalSeconds = Math.round(durationMs / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes === 0) return `${seconds}s`;
  return `${minutes}m ${seconds.toString().padStart(2, '0')}s`;
};

export const statusLabel = (status: BuildStatus) =>
  ({ building: 'Building', uploading: 'Uploading', ready: 'Ready', failed: 'Failed' })[status];

export const artifactLabel = (build: BuildRecord) => build.artifactType.toUpperCase();

// Version and build number as one label: "1.4.0 (57)", or "(57)" alone when
// the app declares no version.
export const buildVersionLabel = (build: BuildRecord) => {
  const { version, buildNumber } = build.metadata;
  if (!buildNumber) return version || `Build ${build.id.slice(0, 8)}`;
  return version ? `${version} (${buildNumber})` : `Build ${buildNumber}`;
};

export const buildFileName = (build: BuildRecord) =>
  `${build.applicationId}-${build.metadata.buildNumber}.${build.artifactType}`;
