import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import zhCN from './zh-CN'
import zhTW from './zh-TW'
import en from './en'
import ja from './ja'
import ko from './ko'

export const LANGUAGES = [
  { code: 'zh-CN', label: '简体中文' },
  { code: 'zh-TW', label: '繁體中文' },
  { code: 'en', label: 'English' },
  { code: 'ja', label: '日本語' },
  { code: 'ko', label: '한국어' },
] as const

export type LangCode = (typeof LANGUAGES)[number]['code']

const savedLang = localStorage.getItem('nebula.lang') || localStorage.getItem('lang') || 'zh-CN'

i18n.use(initReactI18next).init({
  resources: {
    'zh-CN': { translation: zhCN },
    'zh-TW': { translation: zhTW },
    en: { translation: en },
    ja: { translation: ja },
    ko: { translation: ko },
  },
  lng: savedLang,
  fallbackLng: 'zh-CN',
  interpolation: { escapeValue: false },
})

export default i18n
