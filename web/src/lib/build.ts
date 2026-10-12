// Build identity injected by the Makefile (VITE_APP_VERSION, VITE_BUILD_DATE as YYYY-MM-DD).
export const repoUrl = 'https://github.com/ssubbotin/kunveno';

export function buildInfo(env: Record<string, string | undefined>): { version: string; date: string } {
  const iso = env.VITE_BUILD_DATE ?? '';
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  return { version: env.VITE_APP_VERSION || 'dev', date: m ? `${m[3]}.${m[2]}.${m[1]}` : '' };
}

export const build = buildInfo(import.meta.env);
