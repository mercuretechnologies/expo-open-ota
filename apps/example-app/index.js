// BOOT CRASH SIMULATION (temporary): throws during bundle evaluation, before
// any React render, so expo-updates' content-appeared gate never fires and the
// launch is marked failed (rollback + Expo-Recent-Failed-Update-Ids expected).
// global.HermesInternal only exists in the Hermes runtime on device, never in
// Node during `expo export`, so exporting still works.
if (global.HermesInternal) {
  // throw new Error('BOOT CRASH TEST');
}
require('expo-router/entry');
