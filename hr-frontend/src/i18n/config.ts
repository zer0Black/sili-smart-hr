import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';

import en from './locales/en.json';
import zh from './locales/zh.json';

// i18next 配置 + 语言检测。资源按 namespace 组织（common、auth、account），
// zh.json / en.json 同步维护，B 档已渲染可见文案中英两套完整。
void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: 'zh',
    defaultNS: 'common',
    resources: {
      zh,
      en,
    },
    interpolation: { escapeValue: false },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: 'sili-smart-hr-lang',
      caches: ['localStorage'],
    },
  });

export default i18n;
