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
    throw new Error(typeof msg === 'string' ? msg : JSON.stringify(msg))
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
  },

  admin: {
    users: (page = 1, size = 20) =>
      request<ApiResult<{ items: any[]; total: number }>>(`/api/admin/users?page=${page}&size=${size}`),
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
    plugins: () => request<ApiResult<any[]>>('/api/admin/plugins'),
    pluginsStore: () => request<ApiResult<any[]>>('/api/admin/plugins/store'),
    togglePlugin: (id: number | string) =>
      request<ApiResult<any>>(`/api/admin/plugins/${id}/toggle`, { method: 'POST' }),
    testPolicy: (id: number | string, body: any) =>
      request<ApiResult<any>>(`/api/admin/policies/${id}/test`, { method: 'POST', body: JSON.stringify(body) }),
    uploadLogo: (file: File) => {
      const fd = new FormData()
      fd.append('file', file)
      return uploadMultipart('/api/admin/upload-logo', fd)
    },
  },

  tasks: {
    list: () => request<ApiResult<any[]>>('/api/tasks'),
    create: (body: { type: 'http' | 'bt'; url: string; parentId?: number | string | null }) =>
      request<ApiResult<any>>('/api/tasks', { method: 'POST', body: JSON.stringify(body) }),
    cancel: (id: number | string) =>
      request<ApiResult>(`/api/tasks/${id}/cancel`, { method: 'POST' }),
    retry: (id: number | string) =>
      request<ApiResult>(`/api/tasks/${id}/retry`, { method: 'POST' }),
  },

  notifications: {
    list: (page = 1, size = 20) =>
      request<ApiResult<{ items: any[]; total?: number }>>(`/api/notifications?page=${page}&size=${size}`),
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
