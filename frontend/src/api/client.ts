import SparkMD5 from 'spark-md5'

const BASE = ''

export interface AuthUser {
  id?: number | string
  userName: string
  email?: string
  avatar?: string
  [k: string]: any
}

export interface ApiResult<T = any> {
  code: number
  message: string
  data: T
  /** 部分管理端接口附带运行时元信息（如插件宿主是否就绪） */
  meta?: Record<string, any>
}

// ---- 插件相关类型（与后端 pkg/plugin 对应）----

/** 插件运行状态。取自后端 host 包的状态常量。 */
export type PluginStatus =
  | 'running'
  | 'stopped'
  | 'starting'
  | 'stopping'
  | 'crashed'
  | 'failed'
  | 'disabled'

/** 插件列表项：数据库记录 + 实时运行状态。 */
export interface PluginView {
  id: number
  name: string
  title: string
  enabled: boolean
  config: string
  version: string
  author: string
  description: string
  license: string
  homepage: string
  hooks: string
  installSource: string
  lastError: string
  restarts: number
  agreementVersion: number
  installedAt: string
  createdAt: string
  /** 实时状态（后端从插件宿主读，不是数据库字段） */
  status: PluginStatus
  pid: number
  /** 运行时错误，优先于 lastError */
  runtimeError?: string
  restartsNow: number
  hooksList: string[]
  permissions?: string[]
  dir?: string
  /** 磁盘上的清单是否仍可解析（文件被删/损坏时为 false） */
  manifestValid: boolean
  manifestError?: string
  /** 该插件的签名者是否在信任库里（后端 models.Plugin.Trusted，安装时落库） */
  trusted?: boolean
  /** 安装时记录的签名状态（后端 pbclus.Signature）；无签名记录时缺省 */
  signatureInfo?: PluginSignatureInfo
}

/**
 * 包的签名状态。
 *
 * **present / valid / trusted 三者刻意分开**，后端就是这么建模的：
 *  - present  「包里有没有签名块」
 *  - valid    「签名与内容是否自洽」—— **不代表可信**：
 *              Ed25519 密钥任何人都能生成，任何人都能签出一个"有效"的包
 *  - trusted  「签名者的 keyId 是否在宿主的信任库里」
 * 界面必须严格区分 valid 与 trusted，不能把"签名有效"显示成"可信"。
 */
export interface PluginSignatureInfo {
  present: boolean
  valid: boolean
  trusted: boolean
  keyId?: string
  /** 信任库里该密钥的名字；未受信任时为空 */
  keyName?: string
  /** present && !valid 时的原因 */
  error?: string
}

/** 信任库里的一条记录。 */
export interface PluginTrustKey {
  keyId: string
  name: string
  /** base64 公钥；只按 keyId 登记时为空串 */
  publicKey: string
  addedAt: string
}

/**
 * GET /api/admin/plugins/trust 的返回。
 *
 * 注意：信任库文件损坏时后端只返回 mode / path / keys / error，
 * **三个计数字段会缺失** —— 所以它们是可选的，界面不能当成一定有值。
 */
export interface PluginTrustState {
  /** off | warn | require */
  mode: string
  /** 信任库文件绝对路径 */
  path: string
  keys: PluginTrustKey[]
  installedCount?: number
  signedCount?: number
  trustedCount?: number
  /** 非空表示信任库文件损坏 —— 这不是"空库"，必须让管理员看到 */
  error?: string
}

/** 钩子元信息。 */
export interface HookInfo {
  name: string
  /** handler 总数（进程内 + 进程外） */
  count: number
  inProcess: number
  outProcess: number
  doc: string
  /** sync = 阻塞等插件返回；async = 事件通知不等结果 */
  mode: 'sync' | 'async'
  /** error 含 blocked 时能否拦截请求 */
  blockable: boolean
  /** 后端是否已接入该钩子（有实际 Fire 调用点） */
  wired: boolean
  /**
   * 是否还有计划内未完成的工作。
   *
   * 与 wired 正交：wired 说"现在能不能用"，planned 说"计划里还有没有活"。
   * onAntiLeech 就是 wired=true + planned=true —— 它已接线，但漏掉了
   * 公开分享下载这条路径。
   */
  planned: boolean
  /** 计划说明，仅 planned 为 true 时有值 */
  plan: string
}

/** 商店条目。 */
export interface StorePlugin {
  name: string
  title?: string
  version?: string
  author?: string
  description?: string
  url?: string
  license?: string
  homepage?: string
  tags?: string[]
  installed?: boolean
}

/** 协议的一节。 */
export interface AgreementSection {
  heading: string
  paras: string[]
  bullets?: string[]
  critical?: boolean
}

/** 协议全文。 */
export interface Agreement {
  version: number
  title: string
  updated: string
  required: string
  sections: AgreementSection[]
}

/** 同意记录。 */
export interface AgreementRecord {
  version: number
  acceptedAt: string
  acceptedBy: number
  userName: string
  ip: string
}

/** 协议接口返回：全文 + 是否已同意。 */
export interface AgreementState {
  agreement: Agreement
  accepted: boolean
  record: AgreementRecord | null
  version: number
}

// ---- 插件 UI 扩展点（/api/plugins/ui，与后端 pkg/plugin/registry.go 对应）----

/** 插件注册的侧边栏项。 */
export interface PluginSidebarItem {
  plugin: string
  name: string
  title: string
  icon?: string
  area: 'main' | 'admin' | 'public' | string
  role: 'admin' | 'user' | 'any' | string
  order: number
  /** iframe = 站内嵌入；newtab = 新标签页打开 */
  embed: 'iframe' | 'newtab' | string
  /** 后端给的是绝对 URL（可能含 127.0.0.1），前端会归一化成同源相对路径 */
  url: string
  public: boolean
}

/** 插件主题包。 */
export interface PluginThemeDecl {
  name: string
  label?: string
  /** 相对插件目录的 CSS 文件；前端按 /ext/<plugin>/<css> 取 */
  css?: string
  /** CSS 变量覆盖，直接注入 :root */
  variables?: Record<string, string>
  preview?: string
}

/** 插件设置面板字段。 */
export interface PluginSettingField {
  key: string
  label: string
  type: 'text' | 'password' | 'number' | 'bool' | 'select' | 'textarea' | string
  default?: string
  /** select 用，逗号分隔 */
  options?: string
  help?: string
}

/** 注册型能力条目（后台展示用）。 */
export interface PluginProviderInfo {
  kind: string
  name: string
  label?: string
  hook?: string
  plugin: string
}

/** /api/plugins/ui 返回体。 */
export interface PluginUIData {
  protocolVersion: number
  globalEnabled: boolean
  sidebar: { main?: PluginSidebarItem[]; admin?: PluginSidebarItem[] }
  public?: PluginSidebarItem[]
  themes: Record<string, PluginThemeDecl>
  settings: Record<string, PluginSettingField[]>
  providers: PluginProviderInfo[]
  /** 插件通过 onUIThemeApply 动态注入的 CSS 变量 */
  variables: Record<string, any>
  /** 插件通过 onUIPageRegister 动态注入的原始内容 */
  dynamic: Record<string, any>
}

/** 插件全局总开关状态。 */
export interface PluginGlobalState {
  enabled: boolean
  protocolVersion: number
  totalHooks: number
  wiredHooks: number
}

/** 三文件契约诊断结果。 */
export interface PluginContractDiag {
  name: string
  dir: string
  missingFiles: string[]
  required: string[]
  ok: boolean
  error?: string
  protocolVersion?: number
  v1?: boolean
  hooks?: string[]
  runtimeBins?: Record<string, string>
  runtime?: string
  /** co.BEGG 里的原始 PageDecl 列表（字段与 SidebarItem 略有差异） */
  pages?: any[]
}

/** 插件日志条目。 */
export interface PluginLogEntry {
  time: string
  level: 'debug' | 'info' | 'warn' | 'error'
  plugin: string
  message: string
}

export interface LoginData {
  token: string
  user?: AuthUser
  suggest2FAHint?: boolean
  twoFAEnabled?: boolean
}

export interface FileTag {
  id: number
  name: string
  color: string
  fileCount?: number
}

export interface FileVersion {
  id: number
  fileId: number
  version: number
  size: number
  hash?: string
  createdAt: string
  uploaderName?: string
}

export interface FileItem {
  id: number
  ownerId: number
  name: string
  parentId: number | null
  isDir: boolean
  size: number
  policyId: number
  sourceName: string
  extension: string
  mimeType: string
  createdAt: string
  updatedAt: string
  tags?: FileTag[]
  versionCount?: number
}

export interface ShareItem {
  id: number
  fileId: number
  ownerId: number
  downloads: number
  views: number
  expireAt: string | null
  isDir: boolean
  createdAt: string
  // 后端 models.Share.ExtractCode 的 tag 是 json:"extractCode"，会随列表返回。
  // 注意 Password 是 json:"-"，后端永不返回，所以分享列表无法显示密码。
  extractCode?: string
}

// 后端 models.Task 的 status 是 int，不是字符串枚举 —— 展示前必须显式映射。
// 0 等待 / 1 进行 / 2 完成 / 3 失败
export interface TaskItem {
  id: number
  ownerId: number
  type: 'http' | 'bt'
  url: string
  status: 0 | 1 | 2 | 3
  progress: number
  parentId: number | null
  error: string
  createdAt: string
  updatedAt: string
}

// ---- 智能时代相关类型 ----

export interface AIFileBrief {
  id: number
  name: string
  extension: string
  size: number
  mimeType: string
  updatedAt: string
  accessCount: number
  lastAccessed: string
}

export interface SuggestResult {
  suggestions: string[]
  history: string[]
  recent: AIFileBrief[]
  hot: AIFileBrief[]
  mode: string
}

export interface AIStatus {
  enabled: boolean
  available: boolean
  mode: 'local' | 'gateway' | 'plugin'
  codeSpaceEnabled: boolean
  scenes: Record<string, boolean>
  autoSummary: boolean
  autoTag: boolean
  model: string
}

export interface AIConfig {
  enabled: boolean
  baseUrl: string
  apiKey: string
  apiKeySet: boolean
  model: string
  timeoutSec: number
  maxTokens: number
  temperature: number
  provider: string
  codeSpaceEnabled: boolean
  autoSummary: boolean
  autoTag: boolean
  rateLimit: number
  scenes: Record<string, boolean>
  available: boolean
  mode: string
  providers: string[]
}

export interface CompletionItem {
  label: string
  kind: string
  detail: string
  insertText: string
}

export async function request<T = any>(
  url: string,
  options: RequestInit = {},
): Promise<T> {
  const authH = authHeaders()
  const res = await fetch(BASE + url, {
    headers: { 'Content-Type': 'application/json', ...authH, ...(options.headers || {}) },
    ...options,
  })
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const msg = (data && data.message) || res.statusText
    // 非 2xx 的响应体挂到错误对象上，而不是只留 message。
    //
    // 有些接口用 4xx 传递**结构化**信息，调用方必须能读到：
    //   - 409 needConfirm / unsigned（切到 require 前的二次确认）
    //   - 403 needAgreement（插件协议未被接受）
    // 只抛 message 会把它们丢掉，界面就没法提供正确的后续动作。
    const err: any = new Error(typeof msg === 'string' ? msg : JSON.stringify(msg))
    err.status = res.status
    if (data && typeof data === 'object') {
      err.data = data
      err.code = data.code
    }
    throw err
  }
  return data as T
}

function authHeaders(): Record<string, string> {
  const token = localStorage.getItem('nebula_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

/** 以 Bearer 拉取受保护的二进制流（下载 / 预览 / 缩略图） */
export async function fetchBlob(url: string): Promise<Blob> {
  const res = await fetch(BASE + url, { headers: authHeaders() })
  if (!res.ok) throw new Error(res.statusText || 'fetch failed')
  return res.blob()
}

/**
 * 以 query 凭据拉取分享内的文件二进制（公开分享页预览用）。
 *
 * 与 fetchBlob 的区别：不带 Bearer（访问者通常没登录），
 * 而是带 password / extract 走 query —— 后端 readShareAccessParams 认这两种来源。
 * 失败时尽量把后端的 message 带出来：401（需要密码/提取码）与 404（文件不在分享内）
 * 在UI 上要给出完全不同的引导，笼统的 "fetch failed" 没法区分。
 */
export async function fetchShareBlob(url: string, signal?: AbortSignal): Promise<Blob> {
  const res = await fetch(BASE + url, signal ? { signal } : undefined)
  if (!res.ok) {
    let msg = res.statusText || `HTTP ${res.status}`
    try {
      const j = await res.json()
      if (j?.message) msg = j.message
    } catch {
      /* 响应体不是 JSON（网络中断等），沿用状态文本 */
    }
    throw new Error(msg)
  }
  return res.blob()
}

/** 分片读取大文件并以 spark-md5 计算 MD5 */
export function computeMD5(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = (e) => {
      try {
        const result = e.target?.result
        if (!(result instanceof ArrayBuffer)) {
          reject(new Error('read failed'))
          return
        }
        const spark = new SparkMD5.ArrayBuffer()
        spark.append(result)
        resolve(spark.end())
      } catch (err) {
        reject(err)
      }
    }
    reader.onerror = () => reject(reader.error || new Error('read failed'))
    reader.readAsArrayBuffer(file)
  })
}

/** 带 Bearer + 进度回调的 multipart 上传 */
export function uploadWithProgress(
  file: File,
  parentId: number | null,
  hash: string,
  onProgress: (pct: number) => void,
): Promise<ApiResult<FileItem> & { rapid?: boolean }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    const fd = new FormData()
    fd.append('file', file)
    fd.append('parentId', parentId == null ? '' : String(parentId))
    fd.append('hash', hash)
    xhr.open('POST', BASE + '/api/files/upload')
    const token = localStorage.getItem('nebula_token')
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText))
        } catch {
          reject(new Error('upload response parse error'))
        }
      } else {
        reject(new Error(xhr.statusText || `upload failed (${xhr.status})`))
      }
    }
    xhr.onerror = () => reject(new Error('upload network error'))
    xhr.send(fd)
  })
}

export const api = {
  // ---- 插件 UI 扩展点：普通登录用户也能读（后台开关关闭时返回空数据）----
  pluginsUI: () => request<ApiResult<PluginUIData>>('/api/plugins/ui'),
  savePluginUISettings: (plugin: string, values: Record<string, any>) =>
    request<ApiResult<Record<string, any>>>(
      `/api/plugins/ui/settings/${encodeURIComponent(plugin)}`,
      { method: 'POST', body: JSON.stringify(values) },
    ),

  installStatus: () => request<{ installed: boolean }>('/api/install/status'),
  install: (body: any) => request('/api/install', { method: 'POST', body: JSON.stringify(body) }),
  testDB: (body: any) => request<{ code: number; message: string }>('/api/install/test-db', {
    method: 'POST',
    body: JSON.stringify(body),
  }),
  me: () => request<ApiResult<{ user: AuthUser; suggest2FAHint?: boolean }>>('/api/auth/me', { headers: authHeaders() }),
  login: (body: any) =>
    request<ApiResult<LoginData>>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  logout: () => request<ApiResult>('/api/auth/logout', { method: 'POST', headers: authHeaders() }),
  changePassword: (oldPassword: string, newPassword: string) =>
    request<ApiResult>('/api/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({ oldPassword, newPassword }),
      headers: authHeaders(),
    }),

  files: {
    list: (parent?: number | null, trash = false) =>
      request<ApiResult<FileItem[]>>(
        `/api/files?parent=${parent == null ? '' : parent}&trash=${trash ? 1 : 0}`,
        { headers: authHeaders() },
      ),
    breadcrumb: (id: number) =>
      request<ApiResult<FileItem[]>>(`/api/files/breadcrumb/${id}`, { headers: authHeaders() }),
    mkdir: (name: string, parentId: number | null) =>
      request<ApiResult<FileItem>>('/api/files/mkdir', {
        method: 'POST',
        body: JSON.stringify({ name, parentId }),
        headers: authHeaders(),
      }),
    rapid: (hash: string, name: string, parentId: number | null, size: number) =>
      request<ApiResult<FileItem> & { rapid?: boolean }>('/api/files/rapid', {
        method: 'POST',
        body: JSON.stringify({ hash, name, parentId, size }),
        headers: authHeaders(),
      }),
    rename: (id: number, name: string) =>
      request<ApiResult>(`/api/files/${id}/rename`, {
        method: 'PUT',
        body: JSON.stringify({ name }),
        headers: authHeaders(),
      }),
    move: (id: number, parentId: number | null) =>
      request<ApiResult>(`/api/files/${id}/move`, {
        method: 'PUT',
        body: JSON.stringify({ parentId }),
        headers: authHeaders(),
      }),
    remove: (id: number) =>
      request<ApiResult>(`/api/files/${id}`, { method: 'DELETE', headers: authHeaders() }),
    restore: (id: number) =>
      request<ApiResult>(`/api/files/${id}/restore`, { method: 'POST', headers: authHeaders() }),
    purge: (id: number) =>
      request<ApiResult>(`/api/files/${id}/purge`, { method: 'POST', headers: authHeaders() }),
    downloadURL: (id: number) => `/api/files/${id}/download`,
    previewURL: (id: number) => `/api/files/${id}/preview`,
    thumbURL: (id: number) => `/api/files/${id}/thumb`,
    addTag: (fileId: number, name: string, color: string) =>
      request<ApiResult<FileTag>>(`/api/files/${fileId}/tags`, {
        method: 'POST',
        body: JSON.stringify({ name, color }),
        headers: authHeaders(),
      }),
    removeTag: (fileId: number, tagId: number) =>
      request<ApiResult>(`/api/files/${fileId}/tags/${tagId}`, {
        method: 'DELETE',
        headers: authHeaders(),
      }),
    listTags: () =>
      request<ApiResult<FileTag[]>>('/api/files/tags', { headers: authHeaders() }),
    batchMove: (ids: number[], target: number | null) =>
      request<ApiResult>('/api/files/batch-move', {
        method: 'POST',
        body: JSON.stringify({ ids, parentId: target }),
        headers: authHeaders(),
      }),
    batchCopy: (ids: number[], target: number | null) =>
      request<ApiResult>('/api/files/batch-copy', {
        method: 'POST',
        body: JSON.stringify({ ids, parentId: target }),
        headers: authHeaders(),
      }),
    batchPolicy: (ids: number[], policyId: number) =>
      request<ApiResult>('/api/files/batch-policy', {
        method: 'POST',
        body: JSON.stringify({ ids, policyId }),
        headers: authHeaders(),
      }),
    batchDelete: (ids: number[], password?: string) =>
      request<ApiResult>('/api/files/batch-delete', {
        method: 'POST',
        body: JSON.stringify({ ids, password }),
        headers: authHeaders(),
      }),
    listVersions: (fileId: number) =>
      request<ApiResult<FileVersion[]>>(`/api/files/${fileId}/versions`, {
        headers: authHeaders(),
      }),
    uploadVersionURL: (fileId: number) => `/api/files/${fileId}/upload-version`,
    restoreVersion: (fileId: number, versionId: number) =>
      request<ApiResult>(`/api/files/${fileId}/versions/${versionId}/restore`, {
        method: 'POST',
        headers: authHeaders(),
      }),
    deleteVersion: (fileId: number, versionId: number) =>
      request<ApiResult>(`/api/files/${fileId}/versions/${versionId}`, {
        method: 'DELETE',
        headers: authHeaders(),
      }),
    downloadVersionURL: (fileId: number, versionId: number) =>
      `/api/files/${fileId}/versions/${versionId}/download`,
    /** 全局搜索：后端 10 维检索 */
    search: (params: Record<string, string | number | undefined>) => {
      const q = new URLSearchParams()
      Object.entries(params).forEach(([k, v]) => {
        if (v !== undefined && v !== '') q.set(k, String(v))
      })
      return request<ApiResult<{ total: number; list: FileItem[]; page: number; size: number }>>(
        `/api/files/search?${q.toString()}`, { headers: authHeaders() },
      )
    },
    saveSearchHistory: (keyword: string, filters = '') =>
      request<ApiResult>('/api/files/search-history', {
        method: 'POST', body: JSON.stringify({ keyword, filters }), headers: authHeaders(),
      }),
    clearSearchHistory: () =>
      request<ApiResult>('/api/files/search-history', { method: 'DELETE', headers: authHeaders() }),
    /** 智能联想 + 近期常用 + 热门，一次拿齐 */
    suggest: (q: string, limit = 8) =>
      request<ApiResult<SuggestResult>>(`/api/files/suggest?q=${encodeURIComponent(q)}&limit=${limit}`,
        { headers: authHeaders() }),
    recent: (limit = 10) =>
      request<ApiResult<AIFileBrief[]>>(`/api/files/recent?limit=${limit}`, { headers: authHeaders() }),
    hot: (limit = 10) =>
      request<ApiResult<AIFileBrief[]>>(`/api/files/hot?limit=${limit}`, { headers: authHeaders() }),
    clearAccess: () =>
      request<ApiResult>('/api/files/access', { method: 'DELETE', headers: authHeaders() }),
    /** 代码空间：读/写纯文本文件内容 */
    getContent: (id: number) =>
      request<ApiResult<{
        id: number; name: string; content: string; language: string
        size: number; updatedAt: string; version: number
      }>>(`/api/files/${id}/content`, { headers: authHeaders() }),
    saveContent: (id: number, content: string) =>
      request<ApiResult<{ size: number; version: number; updatedAt: string }>>(
        `/api/files/${id}/content`, {
          method: 'PUT', body: JSON.stringify({ content }), headers: authHeaders(),
        }),
  },

  shares: {
    list: () => request<ApiResult<ShareItem[]>>('/api/shares'),
    create: (fileId: number, password?: string, expireAt?: string, extractCode?: string) =>
      request<ApiResult<ShareItem>>('/api/shares', {
        method: 'POST',
        body: JSON.stringify({
          fileId,
          password: password || undefined,
          expireAt: expireAt ? new Date(expireAt).toISOString() : undefined,
          extractCode: extractCode || undefined,
        }),
      }),
    remove: (id: number) => request<ApiResult>(`/api/shares/${id}`, { method: 'DELETE' }),
    detail: (id: number | string, password?: string, extractCode?: string) =>
      request<ApiResult<any>>(`/api/shares/${id}`, {
        method: 'POST',
        body: JSON.stringify({ password, extractCode }),
      }),
    // 下载/预览的裸 URL：给 <a download>、<img>、<video> 这类需要自己发请求的地方用。
    // 密码/提取码走 query 而不是 header —— <img src> 没法带自定义 header，
    // 这是唯一能让浏览器原生媒体标签直接吃这个端点的方式。
    fileURL: (
      id: number | string,
      opts?: { fileId?: number | string; password?: string; extractCode?: string; preview?: boolean },
    ) => {
      const q = new URLSearchParams()
      if (opts?.fileId != null) q.set('fileId', String(opts.fileId))
      if (opts?.password) q.set('password', opts.password)
      if (opts?.extractCode) q.set('extract', opts.extractCode)
      const qs = q.toString()
      const base = `/api/shares/${id}/${opts?.preview ? 'preview' : 'download'}`
      return qs ? `${base}?${qs}` : base
    },
  },

  admin: {
    users: (page = 1, size = 20) =>
      request<ApiResult<{ list: any[]; total: number }>>(`/api/admin/users?page=${page}&size=${size}`),
    createUser: (body: any) =>
      request<ApiResult<any>>('/api/admin/users', { method: 'POST', body: JSON.stringify(body) }),
    updateUser: (id: number | string, body: any) =>
      request<ApiResult<any>>(`/api/admin/users/${id}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    deleteUser: (id: number | string) =>
      request<ApiResult>(`/api/admin/users/${id}`, { method: 'DELETE' }),
    groups: () => request<ApiResult<any[]>>('/api/admin/groups'),
    createGroup: (body: any) =>
      request<ApiResult<any>>('/api/admin/groups', { method: 'POST', body: JSON.stringify(body) }),
    updateGroup: (id: number | string, body: any) =>
      request<ApiResult<any>>(`/api/admin/groups/${id}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    policies: () => request<ApiResult<any[]>>('/api/admin/policies'),
    createPolicy: (body: any) =>
      request<ApiResult<any>>('/api/admin/policies', { method: 'POST', body: JSON.stringify(body) }),
    updatePolicy: (id: number | string, body: any) =>
      request<ApiResult<any>>(`/api/admin/policies/${id}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    deletePolicy: (id: number | string) =>
      request<ApiResult>(`/api/admin/policies/${id}`, { method: 'DELETE' }),
    settings: () => request<ApiResult<Record<string, any>>>('/api/admin/settings'),
    saveSettings: (body: Record<string, any>) =>
      request<ApiResult>('/api/admin/settings', {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    dashboard: () => request<ApiResult<any>>('/api/admin/dashboard'),
    trafficStats: (days: number) =>
      request<ApiResult<any[]>>(`/api/admin/stats/traffic?days=${days}`),
    fileTypeStats: () => request<ApiResult<any[]>>('/api/admin/stats/file-types'),
    activityStats: (days: number) =>
      request<ApiResult<any[]>>(`/api/admin/stats/activity?days=${days}`),
    // ---- 插件（进程外子进程体系）----
    // 注意：启停是热加载（拉起/杀掉子进程），主服务不重启。
    // 旧的 togglePlugin 已随后端接口一起移除。
    plugins: () => request<ApiResult<PluginView[]>>('/api/admin/plugins'),
    pluginsHooks: () => request<ApiResult<HookInfo[]>>('/api/admin/plugins/hooks'),
    pluginsStore: () => request<ApiResult<StorePlugin[]>>('/api/admin/plugins/store'),
    pluginAgreement: () => request<ApiResult<AgreementState>>('/api/admin/plugins/agreement'),
    acceptPluginAgreement: (version: number) =>
      request<ApiResult<AgreementRecord>>('/api/admin/plugins/agreement', {
        method: 'POST',
        body: JSON.stringify({ version, accept: true }),
      }),
    installPlugin: (body: { source: 'local' | 'url'; path?: string; url?: string }) =>
      request<ApiResult<{ plugin: any; message?: string }>>('/api/admin/plugins/install', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    uninstallPlugin: (name: string, confirmPassword: string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${encodeURIComponent(name)}/uninstall`, {
        method: 'POST',
        // 卸载会杀掉子进程并删除插件目录，后端要求二次确认密码
        headers: { 'X-Confirm-Password': confirmPassword },
      }),
    enablePlugin: (name: string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${encodeURIComponent(name)}/enable`, { method: 'POST' }),
    disablePlugin: (name: string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${encodeURIComponent(name)}/disable`, { method: 'POST' }),
    restartPlugin: (name: string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${encodeURIComponent(name)}/restart`, { method: 'POST' }),
    updatePluginConfig: (name: string, config: string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${encodeURIComponent(name)}/config`, {
        method: 'PUT',
        body: JSON.stringify({ config }),
      }),
    pluginLogs: (name?: string, limit = 200) =>
      request<ApiResult<PluginLogEntry[]>>(
        `/api/admin/plugins/logs?limit=${limit}${name ? `&name=${encodeURIComponent(name)}` : ''}`,
      ),
    // ---- v2 插件平台：全局总开关与三文件契约诊断 ----
    pluginsGlobal: () =>
      request<ApiResult<PluginGlobalState>>('/api/admin/plugins/global'),
    // 开启必须 acknowledge=true：后端会强制校验，前端必须先弹风险确认。
    setPluginsGlobal: (enabled: boolean, acknowledge = false) =>
      request<ApiResult<{ enabled: boolean }>>('/api/admin/plugins/global', {
        method: 'POST',
        body: JSON.stringify({ enabled, acknowledge }),
      }),
    pluginContract: (name: string) =>
      request<ApiResult<PluginContractDiag>>(
        `/api/admin/plugins/contract/${encodeURIComponent(name)}`,
      ),

    // ---- 签名与信任 ----
    // 接口只接受**公钥**：私钥贴进来后端会用 400 明确挡住（message 含「私钥」），
    // 界面要把那条 message 原样展示，不能吞掉。
    pluginTrust: () => request<ApiResult<PluginTrustState>>('/api/admin/plugins/trust'),
    addPluginTrustKey: (body: { publicKey?: string; keyId?: string; name: string; force?: boolean }) =>
      request<ApiResult<{ keyId: string; name: string; count: number; warning?: string }>>(
        '/api/admin/plugins/trust',
        { method: 'POST', body: JSON.stringify(body) },
      ),
    removePluginTrustKey: (keyId: string) =>
      request<ApiResult<{ keyId: string; count: number; message: string }>>(
        `/api/admin/plugins/trust/${encodeURIComponent(keyId)}`,
        { method: 'DELETE' },
      ),
    // 切到 require 且存在无签名记录的插件时，后端返回 409 + needConfirm，
    // 需要带 confirm=true 重发（见 request()：4xx 的响应体挂在 error.data 上）。
    setPluginTrustMode: (mode: string, confirm?: boolean) =>
      request<ApiResult<{ mode: string; message: string }>>('/api/admin/plugins/trust/mode', {
        method: 'PUT',
        body: JSON.stringify({ mode, confirm: !!confirm }),
      }),

    testPolicy: (id: number | string, body: any) =>
      request<ApiResult<any>>(`/api/admin/policies/${id}/test`, { method: 'POST', body: JSON.stringify(body) }),
    uploadLogo: (file: File) => {
      const fd = new FormData()
      fd.append('file', file)
      return uploadMultipart('/api/admin/upload-logo', fd)
    },
  },

  tasks: {
    // 后端 ListTasks 对非管理员按 owner_id 过滤、对管理员返回全部（硬编码 LIMIT 100），
    // 所以这个接口同时服务用户页与管理员页。
    list: () => request<ApiResult<TaskItem[]>>('/api/tasks'),
    create: (body: { type: 'http' | 'bt'; url: string; parentId?: number | string | null }) =>
      request<ApiResult<TaskItem>>('/api/tasks', { method: 'POST', body: JSON.stringify(body) }),
    cancel: (id: number | string) =>
      request<ApiResult>(`/api/tasks/${id}/cancel`, { method: 'POST' }),
    retry: (id: number | string) =>
      request<ApiResult>(`/api/tasks/${id}/retry`, { method: 'POST' }),
  },

  // 回收站：后端没有独立的 /api/trash 分组，全部复用 /api/files 的 trash 模式。
  //   GET  /api/files?trash=1            列出该用户全部软删除文件（跨目录扁平，忽略 parent）
  //   POST /api/files/:id/restore        还原
  //   POST /api/files/:id/purge          彻底删除（释放配额 + 删物理文件）
  //
  // 已知限制：File.DeletedAt 的 tag 是 json:"-"，所以列表拿不到删除时间；
  // Restore 每次都会新建一个「恢复的文件-YYYYMMDD-HHMMSS」目录。
  trash: {
    list: () => request<ApiResult<FileItem[]>>('/api/files?parent=&trash=1'),
    restore: (id: number) => request<ApiResult>(`/api/files/${id}/restore`, { method: 'POST' }),
    purge: (id: number) => request<ApiResult>(`/api/files/${id}/purge`, { method: 'POST' }),
  },

  notifications: {
    list: (page = 1, size = 20) =>
      request<ApiResult<{ list: any[]; total?: number }>>(`/api/notifications?page=${page}&size=${size}`),
    unreadCount: () => request<ApiResult<number>>('/api/notifications/unread-count'),
    markRead: (id: number | string) =>
      request<ApiResult>(`/api/notifications/${id}/read`, { method: 'PUT', headers: authHeaders() }),
    markAllRead: () =>
      request<ApiResult>('/api/notifications/read-all', { method: 'PUT', headers: authHeaders() }),
    remove: (id: number | string) =>
      request<ApiResult>(`/api/notifications/${id}`, { method: 'DELETE', headers: authHeaders() }),
  },

  oauth: {
    authorize: (body: { clientId: string; redirectUri: string; scope?: string; state?: string }) =>
      request<ApiResult<{ code: string }>>('/api/oauth/authorize', {
        method: 'POST',
        body: JSON.stringify(body),
        headers: authHeaders(),
      }),
  },

  tokens: {
    list: () => request<ApiResult<any[]>>('/api/tokens', { headers: authHeaders() }),
    create: (body: { name: string; expiresInDays?: number; scopes?: string[] }) =>
      request<ApiResult<{ token: string; id?: number | string; prefix?: string }>>('/api/tokens', {
        method: 'POST',
        body: JSON.stringify(body),
        headers: authHeaders(),
      }),
    revoke: (id: number | string) =>
      request<ApiResult>(`/api/tokens/${id}`, { method: 'DELETE', headers: authHeaders() }),
  },

  /** 智能时代：AI 场景 + 后台配置 */
  ai: {
    status: () => request<ApiResult<AIStatus>>('/api/ai/status', { headers: authHeaders() }),
    search: (query: string, limit = 20) =>
      request<ApiResult<{
        answer: string
        filters: Record<string, any>
        list: FileItem[]
        mode: string
      }>>('/api/ai/search', {
        method: 'POST', body: JSON.stringify({ query, limit }), headers: authHeaders(),
      }),
    ask: (question: string, history: string[] = []) =>
      request<ApiResult<{ answer: string; mode: string; refs: FileItem[] }>>('/api/ai/ask', {
        method: 'POST', body: JSON.stringify({ question, history }), headers: authHeaders(),
      }),
    complete: (payload: {
      fileId?: number; fileName?: string; language?: string
      prefix?: string; suffix?: string; doc?: string; maxTokens?: number
    }) =>
      request<ApiResult<{ items: CompletionItem[]; text: string; mode: string; hint: string }>>(
        '/api/ai/complete', {
          method: 'POST', body: JSON.stringify(payload), headers: authHeaders(),
        }),
    summary: (id: number) =>
      request<ApiResult<{
        summary: string; tags: string[]; provider: string; cached: boolean; updatedAt: string
      }>>(`/api/ai/summary/${id}`, { method: 'POST', headers: authHeaders() }),
    getConfig: () =>
      request<ApiResult<AIConfig>>('/api/admin/ai/config', { headers: authHeaders() }),
    saveConfig: (cfg: Partial<AIConfig>) =>
      request<ApiResult<AIConfig>>('/api/admin/ai/config', {
        method: 'POST', body: JSON.stringify(cfg), headers: authHeaders(),
      }),
    testConfig: (cfg: Partial<AIConfig>) =>
      request<ApiResult<{ reply: string }>>('/api/admin/ai/test', {
        method: 'POST', body: JSON.stringify(cfg), headers: authHeaders(),
      }),
    usage: () =>
      request<ApiResult<{
        total: number; success: number; failed: number; tokens: number
        avgLatencyMs: number; last7Days: number
        byScene: Record<string, number>; byProvider: Record<string, number>
      }>>('/api/admin/ai/usage', { headers: authHeaders() }),
    logs: (limit = 50) =>
      request<ApiResult<any[]>>(`/api/admin/ai/logs?limit=${limit}`, { headers: authHeaders() }),
  },
}

/** 带 Bearer 的 multipart 上传（如 logo），不预设 Content-Type，由 FormData 自动设置 */
export function uploadMultipart(url: string, fd: FormData): Promise<ApiResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', BASE + url)
    const token = localStorage.getItem('nebula_token')
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try { resolve(JSON.parse(xhr.responseText)) } catch { reject(new Error('upload response parse error')) }
      } else {
        reject(new Error(xhr.statusText || `upload failed (${xhr.status})`))
      }
    }
    xhr.onerror = () => reject(new Error('upload network error'))
    xhr.send(fd)
  })
}
