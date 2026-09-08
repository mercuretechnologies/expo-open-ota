import { describe, expect, it } from 'vitest';

import { checkEnvironment, mergeEnvironment } from '../environment';

describe('build environment', () => {
  it('overrides only supplied keys and injects channel before checking', () => {
    expect(mergeEnvironment({ A: 'remote' }, { B: 'file' }, 'production')).toEqual({
      A: 'remote',
      B: 'file',
      RELEASE_CHANNEL: 'production',
    });
    expect(mergeEnvironment({ RELEASE_CHANNEL: '' }, {}, 'production').RELEASE_CHANNEL).toBe('');
  });
  it('checks dot, bracket, optional access and destructuring without values', () => {
    expect(() => {
      checkEnvironment(
        'process.env.API; process.env["OTHER"]; process.env?.THIRD; const {FOURTH}=process.env;',
        'app.ts',
        ['API']
      );
    }).toThrow('FOURTH, OTHER, THIRD');
    expect(() => {
      checkEnvironment('process.env.API; process.env.NODE_ENV', 'app.ts', ['API']);
    }).not.toThrow();
  });
  it('refuses dynamic references and aliases instead of claiming coverage', () => {
    expect(() => {
      checkEnvironment('process.env[name]', 'app.ts', []);
    }).toThrow('dynamic');
    expect(() => {
      checkEnvironment('const env=process.env; env.API', 'app.ts', []);
    }).toThrow('dynamic');
  });
  it('rejects aliases of process itself', () => {
    expect(() => {
      checkEnvironment('const p=process; p.env.API', 'app.ts', []);
    }).toThrow('dynamic');
  });
  it('does not mistake strings or comments for references', () => {
    expect(() => {
      checkEnvironment('const s="process.env.SECRET"; // process.env.MISSING', 'app.ts', []);
    }).not.toThrow();
  });
});
