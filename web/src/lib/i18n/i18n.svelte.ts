// Current language and the translation function. The language lives in $state, so every
// template that calls t() re-renders when it changes.
import { LANGS, LANG_TAGS, messages, type Key, type Lang } from './messages';

const STORE = 'kunveno.lang';
type Vars = Record<string, string | number>;

// initialLang: Russian by default, or the language the person chose before.
export function initialLang(saved: string | null): Lang {
  return (LANGS as readonly string[]).includes(saved ?? '') ? (saved as Lang) : 'ru';
}

export function translate(lang: Lang, key: Key, vars: Vars = {}): string {
  const entry = messages[lang][key] ?? messages.ru[key];
  const text = typeof entry === 'string'
    ? entry
    : (entry[new Intl.PluralRules(LANG_TAGS[lang]).select(Number(vars.n)) as keyof typeof entry] ?? entry.other);
  return text.replace(/\{(\w+)\}/g, (all, name: string) => (name in vars ? String(vars[name]) : all));
}

class I18n {
  lang = $state<Lang>(initialLang(typeof localStorage === 'undefined' ? null : localStorage.getItem(STORE)));
}

export const i18n = new I18n();

export function t(key: Key, vars?: Vars): string {
  return translate(i18n.lang, key, vars);
}

export function setLang(lang: Lang): void {
  i18n.lang = lang;
  localStorage.setItem(STORE, lang);
  document.documentElement.lang = lang;
}

// formatDate shows an ISO date (YYYY-MM-DD) the way the current language writes dates.
export function formatDate(iso: string): string {
  const d = new Date(iso + 'T00:00:00');
  return Number.isNaN(d.getTime()) ? iso : new Intl.DateTimeFormat(LANG_TAGS[i18n.lang], { dateStyle: 'long' }).format(d);
}
