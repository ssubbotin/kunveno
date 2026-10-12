import { describe, expect, it } from 'vitest';
import { buildInfo } from './build';

describe('buildInfo', () => {
  it('reads version and date injected at build time', () => {
    expect(buildInfo({ VITE_APP_VERSION: '0.0.0+git68af9bf', VITE_BUILD_DATE: '2026-10-12' }))
      .toEqual({ version: '0.0.0+git68af9bf', iso: '2026-10-12' });
  });
  it('falls back to dev for local runs', () => {
    expect(buildInfo({})).toEqual({ version: 'dev', iso: '' });
  });
});
