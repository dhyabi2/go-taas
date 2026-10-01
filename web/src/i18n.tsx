// Lightweight i18n for the console (no external dependency). Provides a
// language context, a t() translation function, and a language switch.
// Translations live in ./locales/{en,zh}.ts.

import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { en } from './locales/en';
import { zh } from './locales/zh';

export type Lang = 'en' | 'zh';

const STORAGE_KEY = 'go-taas.lang';

const dictionaries: Record<Lang, Record<string, string>> = { en, zh };

export function getStoredLang(): Lang {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'zh' || stored === 'en') return stored;
  // Default to the browser language when it is Chinese, else English.
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

export function setStoredLang(lang: Lang): void {
  localStorage.setItem(STORAGE_KEY, lang);
}

interface I18nContextValue {
  lang: Lang;
  setLang: (lang: Lang) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nContextValue>({
  lang: 'en',
  setLang: () => {},
  t: (key) => key,
});

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(getStoredLang);

  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);

  const setLang = (next: Lang) => {
    setStoredLang(next);
    setLangState(next);
  };

  const t = (key: string, vars?: Record<string, string | number>): string => {
    let text = dictionaries[lang][key] ?? dictionaries.en[key] ?? key;
    if (vars) {
      for (const [k, v] of Object.entries(vars)) {
        text = text.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v));
      }
    }
    return text;
  };

  return (
    <I18nContext.Provider value={{ lang, setLang, t }}>{children}</I18nContext.Provider>
  );
}

export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}