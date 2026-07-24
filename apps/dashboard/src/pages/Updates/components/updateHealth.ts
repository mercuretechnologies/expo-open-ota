import { UpdateHealthRecord } from '@/lib/api';

// Publish groups and rollouts can span one update per platform. Aggregate the
// raw success/failure cohorts, not percentages: runtime crashers are still in
// devicesOnUpdate and would otherwise be counted as both healthy and faulty.
export const aggregateUpdateHealth = (
  entries: Array<UpdateHealthRecord | undefined>
): UpdateHealthRecord | undefined => {
  const available = entries.filter((entry): entry is UpdateHealthRecord => !!entry);
  if (available.length === 0) return undefined;

  const devicesOnUpdate = available.reduce((total, entry) => total + entry.devicesOnUpdate, 0);
  const successfulDevices = available.reduce((total, entry) => total + entry.successfulDevices, 0);
  const faultyDevices = available.reduce((total, entry) => total + entry.faultyDevices, 0);
  const attempts = successfulDevices + faultyDevices;

  return {
    devicesOnUpdate,
    successfulDevices,
    faultyDevices,
    healthPercent: attempts > 0 ? (100 * successfulDevices) / attempts : null,
  };
};
