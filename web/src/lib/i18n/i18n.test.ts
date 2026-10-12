import { describe, expect, it } from 'vitest';
import { LANGS, messages, type Plural } from './messages';
import { initialLang, translate } from './i18n.svelte';

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
const texts = (e: string | Plural) => (typeof e === 'string' ? [e] : Object.values(e).filter((v): v is string => !!v));

describe('dictionaries', () => {
  it('have the same keys and placeholders in every language', () => {
    const keys = Object.keys(messages.ru).sort();
    for (const lang of LANGS) {
      expect(Object.keys(messages[lang]).sort(), lang).toEqual(keys);
      for (const k of keys) {
        const want = placeholders(texts(messages.ru[k as keyof typeof messages.ru])[0]);
        for (const text of texts(messages[lang][k as keyof typeof messages.ru])) {
          expect(placeholders(text), `${lang} ${k}`).toEqual(want);
        }
      }
    }
  });

  it('use no long dash and no empty strings', () => {
    for (const lang of LANGS) for (const e of Object.values(messages[lang])) for (const s of texts(e)) {
      expect(s.includes('—'), s).toBe(false);
      expect(s.trim(), lang).not.toBe('');
    }
  });
});

describe('translate', () => {
  it('fills placeholders', () => {
    expect(translate('en', 'enter.join', { room: 'Testing' })).toBe('Join “Testing”');
    expect(translate('es', 'pin.tooMany', { seconds: 60 })).toBe('Demasiados PIN incorrectos. Inténtalo de nuevo en 60 s.');
  });

  it('picks the plural form of each language', () => {
    expect([1, 2, 5, 11, 21, 22].map((n) => translate('ru', 'room.people', { n }))).toEqual(
      ['1 участник', '2 участника', '5 участников', '11 участников', '21 участник', '22 участника']);
    expect([1, 2].map((n) => translate('en', 'room.people', { n }))).toEqual(['1 participant', '2 participants']);
    expect([1, 3].map((n) => translate('es', 'room.people', { n }))).toEqual(['1 participante', '3 participantes']);
  });
});

describe('initialLang', () => {
  it('is Russian unless another supported language was chosen', () => {
    expect(initialLang(null)).toBe('ru');
    expect(initialLang('de')).toBe('ru');
    expect(initialLang('es')).toBe('es');
  });
});
