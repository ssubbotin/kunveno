// Build identity injected by the Makefile (VITE_APP_VERSION, VITE_BUILD_DATE as YYYY-MM-DD).
export const repoUrl = 'https://github.com/ssubbotin/kunveno';

// The date is kept as ISO (YYYY-MM-DD); the footer formats it in the current language.
export function buildInfo(env: Record<string, string | undefined>): { version: string; iso: string } {
  const iso = env.VITE_BUILD_DATE ?? '';
  return { version: env.VITE_APP_VERSION || 'dev', iso: /^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso : '' };
}

export const build = buildInfo(import.meta.env);
