/*
 * NebulaDrive Service Worker —— 只缓存「应用壳」，绝不碰用户数据。
 *
 * 设计边界（改之前先读）：
 *   - 不缓存任何 /api/* 请求：文件列表、预览、下载都带用户态/签名，
 *     缓存下来既会串号也会泄露隐私。
 *   - 不缓存 Range 请求、不拦截 POST/PUT/DELETE。
 *   - 导航请求用 network-first：上线时有网就拿最新页面；离线时回退到
 *     缓存的 index.html，让用户至少能看到界面提示（数据请求会失败，
 *     但那是预期的 —— 网盘离线本来就不该有数据）。
 *   - 静态资源（带 hash 的 JS/CSS/图标）用 cache-first，省流量。
 *   - cache 名带版本号，升级时自动清掉旧缓存。
 */
const CACHE_VERSION = 'nebula-shell-v1';
const SHELL_CACHE = CACHE_VERSION + '-static';

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) => cache.addAll(['/', '/index.html', '/manifest.webmanifest'])).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('nebula-shell-') && k !== SHELL_CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  // 只处理同源；跨域（例如 CDN）一律交给浏览器，不插手。
  if (url.origin !== self.location.origin) return;

  // API、Range、下载/预览/缩略图：完全不碰，直接走网络。
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/cdn/')) return;
  if (req.headers.has('range')) return;

  // 导航请求：network-first，离线回退缓存的 index.html（SPA 路由应用壳）。
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).then((resp) => {
        if (resp && resp.ok) {
          const copy = resp.clone();
          caches.open(SHELL_CACHE).then((cache) => cache.put('/index.html', copy));
        }
        return resp;
      }).catch(() => caches.match('/index.html'))
    );
    return;
  }

  // 静态资源（JS/CSS/图标/manifest）：cache-first，命中即返回。
  event.respondWith(
    caches.match(req).then((cached) => {
      if (cached) return cached;
      return fetch(req).then((resp) => {
        if (resp && resp.ok && resp.type === 'basic') {
          const copy = resp.clone();
          caches.open(SHELL_CACHE).then((cache) => cache.put(req, copy));
        }
        return resp;
      });
    })
  );
});
