import { create } from 'zustand'

export type SelectMode = 'context' | 'floating' | 'drawer'
export type FileView = 'list' | 'grid'
export type SortKey = 'name' | 'size' | 'modified'
export type Density = 'compact' | 'normal' | 'comfortable'

/** 用户可自定义偏好。全部落 localStorage，逐项都有默认值，旧配置缺字段时自动补默认。 */
interface UserPrefsState {
  // --- 原有字段（保持兼容）---
  logoEgg: boolean
  eggMode: boolean
  selectMode: SelectMode
  suggest2FAHint: boolean
  twoFAEnabled: boolean
  avatarHalo: boolean

  // --- 界面 / 外观 ---
  density: Density          // 列表密度
  showFileIcons: boolean    // 文件类型图标
  showFileSize: boolean     // 显示文件大小
  showFileDate: boolean     // 显示修改时间
  showHiddenFiles: boolean  // 显示 . 开头文件
  compactNumbers: boolean   // 大小/时间用紧凑格式
  reduceMotion: boolean     // 减少动画

  // --- 文件浏览 ---
  fileView: FileView        // 列表 / 网格
  sortBy: SortKey           // 排序字段
  sortDesc: boolean         // 降序
  pageSize: number          // 每页条数
  gridSize: number          // 网格图标尺寸

  // --- 交互 ---
  doubleClickOpen: boolean  // 双击打开
  confirmDelete: boolean    // 删除前确认
  autoPlayPreview: boolean  // 自动播放预览
  rememberLastPath: boolean // 记住上次目录

  // --- 高级 ---
  showStorageBar: boolean   // 显示容量条
  showVersionBadge: boolean // 显示版本数角标
  enableShortcuts: boolean  // 键盘快捷键

  // --- actions ---
  setLogoEgg: (v: boolean) => void
  setEggMode: (v: boolean) => void
  setSelectMode: (m: SelectMode) => void
  set2FAHint: (v: boolean) => void
  setTwoFAEnabled: (v: boolean) => void
  setAvatarHalo: (v: boolean) => void
  set: <K extends keyof PrefsData>(key: K, value: PrefsData[K]) => void
  reset: () => void
  load: () => void
  save: () => void
}

type PrefsData = Omit<UserPrefsState,
  'set' | 'reset' | 'load' | 'save' | 'setLogoEgg' | 'setEggMode' | 'setSelectMode' |
  'set2FAHint' | 'setTwoFAEnabled' | 'setAvatarHalo'>

const PREFS_KEY = 'nebula.prefs'

const defaultState: PrefsData = {
  logoEgg: false,
  eggMode: false,
  selectMode: 'context',
  suggest2FAHint: false,
  twoFAEnabled: false,
  avatarHalo: false,

  density: 'normal',
  showFileIcons: true,
  showFileSize: true,
  showFileDate: true,
  showHiddenFiles: false,
  compactNumbers: true,
  reduceMotion: false,

  fileView: 'list',
  sortBy: 'modified',
  sortDesc: true,
  pageSize: 50,
  gridSize: 96,

  doubleClickOpen: true,
  confirmDelete: true,
  autoPlayPreview: false,
  rememberLastPath: true,

  showStorageBar: true,
  showVersionBadge: true,
  enableShortcuts: true,
}

// 校验器：localStorage 里的值可能来自旧版本或被手改，非法值一律回退默认。
const SELECT_MODES: SelectMode[] = ['context', 'floating', 'drawer']
const FILE_VIEWS: FileView[] = ['list', 'grid']
const SORT_KEYS: SortKey[] = ['name', 'size', 'modified']
const DENSITIES: Density[] = ['compact', 'normal', 'comfortable']

function coercePrefs(raw: any): PrefsData {
  const d = defaultState
  const b = (v: any, fallback: boolean) => (typeof v === 'boolean' ? v : fallback)
  const n = (v: any, fallback: number, min: number, max: number) => {
    const x = Number(v)
    return Number.isFinite(x) ? Math.min(max, Math.max(min, x)) : fallback
  }
  const inList = <T,>(v: any, list: T[], fallback: T): T =>
    list.includes(v as T) ? (v as T) : fallback
  return {
    logoEgg: b(raw.logoEgg, d.logoEgg),
    eggMode: raw.eggMode != null ? b(raw.eggMode, d.eggMode) : b(raw.logoEgg, d.eggMode),
    selectMode: inList(raw.selectMode, SELECT_MODES, d.selectMode),
    suggest2FAHint: b(raw.suggest2FAHint, d.suggest2FAHint),
    twoFAEnabled: b(raw.twoFAEnabled, d.twoFAEnabled),
    avatarHalo: b(raw.avatarHalo, d.avatarHalo),

    density: inList(raw.density, DENSITIES, d.density),
    showFileIcons: b(raw.showFileIcons, d.showFileIcons),
    showFileSize: b(raw.showFileSize, d.showFileSize),
    showFileDate: b(raw.showFileDate, d.showFileDate),
    showHiddenFiles: b(raw.showHiddenFiles, d.showHiddenFiles),
    compactNumbers: b(raw.compactNumbers, d.compactNumbers),
    reduceMotion: b(raw.reduceMotion, d.reduceMotion),

    fileView: inList(raw.fileView, FILE_VIEWS, d.fileView),
    sortBy: inList(raw.sortBy, SORT_KEYS, d.sortBy),
    sortDesc: b(raw.sortDesc, d.sortDesc),
    pageSize: n(raw.pageSize, d.pageSize, 10, 200),
    gridSize: n(raw.gridSize, d.gridSize, 64, 200),

    doubleClickOpen: b(raw.doubleClickOpen, d.doubleClickOpen),
    confirmDelete: b(raw.confirmDelete, d.confirmDelete),
    autoPlayPreview: b(raw.autoPlayPreview, d.autoPlayPreview),
    rememberLastPath: b(raw.rememberLastPath, d.rememberLastPath),

    showStorageBar: b(raw.showStorageBar, d.showStorageBar),
    showVersionBadge: b(raw.showVersionBadge, d.showVersionBadge),
    enableShortcuts: b(raw.enableShortcuts, d.enableShortcuts),
  }
}

export const useUserPrefsStore = create<UserPrefsState>((setState, get) => ({
  ...defaultState,
  load: () => {
    try {
      const raw = localStorage.getItem(PREFS_KEY)
      if (raw) setState(coercePrefs(JSON.parse(raw)))
    } catch {
      // 配置损坏时保持默认值，不阻断启动
    }
  },
  save: () => {
    const s = get()
    const data: PrefsData = {
      logoEgg: s.logoEgg, eggMode: s.eggMode, selectMode: s.selectMode,
      suggest2FAHint: s.suggest2FAHint, twoFAEnabled: s.twoFAEnabled, avatarHalo: s.avatarHalo,
      density: s.density, showFileIcons: s.showFileIcons, showFileSize: s.showFileSize,
      showFileDate: s.showFileDate, showHiddenFiles: s.showHiddenFiles,
      compactNumbers: s.compactNumbers, reduceMotion: s.reduceMotion,
      fileView: s.fileView, sortBy: s.sortBy, sortDesc: s.sortDesc,
      pageSize: s.pageSize, gridSize: s.gridSize,
      doubleClickOpen: s.doubleClickOpen, confirmDelete: s.confirmDelete,
      autoPlayPreview: s.autoPlayPreview, rememberLastPath: s.rememberLastPath,
      showStorageBar: s.showStorageBar, showVersionBadge: s.showVersionBadge,
      enableShortcuts: s.enableShortcuts,
    }
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(data))
    } catch {
      // localStorage 满/禁用时静默降级
    }
  },
  set: (key, value) => {
    setState({ [key]: value } as any)
    get().save()
  },
  reset: () => {
    setState({ ...defaultState })
    get().save()
  },
  setLogoEgg: (v) => get().set('logoEgg', v),
  setEggMode: (v) => {
    setState({ logoEgg: v, eggMode: v })
    get().save()
  },
  setSelectMode: (m) => get().set('selectMode', m),
  set2FAHint: (v) => get().set('suggest2FAHint', v),
  setTwoFAEnabled: (v) => get().set('twoFAEnabled', v),
  setAvatarHalo: (v) => get().set('avatarHalo', v),
}))
