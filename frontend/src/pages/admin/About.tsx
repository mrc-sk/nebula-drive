import { useEffect, useState } from 'react'
import { ArrowLeftRight, Github, Heart, Code2, Server, Database, CloudLightning, Shield, Sparkles, Info } from 'lucide-react'
import { getBrand } from '../../App'
import type { BrandInfo } from '../../App'
import { api } from '../../api/client'

const STACK = [
  { name: 'React 18', Icon: Code2 },
  { name: 'TypeScript', Icon: Code2 },
  { name: 'Vite', Icon: Server },
  { name: 'TailwindCSS', Icon: Sparkles },
  { name: 'Zustand', Icon: Database },
  { name: 'i18next', Icon: CloudLightning },
  { name: 'React Router', Icon: Shield },
  { name: 'Lucide Icons', Icon: Sparkles },
]

export default function About() {
  const [brand, setBrand] = useState<BrandInfo>({
    name: 'NebulaDrive',
    author: 'NebulaDrive Team',
    version: '1.0.0',
  })

  useEffect(() => {
    setBrand(getBrand())
    let alive = true
    const load = async () => {
      try {
        const r = await (api.admin as any).settings?.()
        if (alive && r?.code === 0 && r.data) {
          const d = r.data || {}
          setBrand({
            name: d['brand.name'] || d.brandName || 'NebulaDrive',
            author: d['brand.author'] || d.brandAuthor || 'NebulaDrive Team',
            version: d['brand.version'] || d.brandVersion || '1.0.0',
          })
        }
      } catch {}
    }
    load()
    return () => { alive = false }
  }, [])

  const year = new Date().getFullYear()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">关于</h1>
        <p className="text-sm text-slate-400">项目信息 · 合规声明 · 致谢</p>
      </div>

      <div className="glass-strong mx-auto max-w-2xl rounded-3xl p-8 shadow-glass-lg">
        <div className="flex flex-col items-center text-center">
          <div className="relative mb-4">
            <div className="grid h-20 w-20 place-items-center rounded-3xl bg-gradient-to-br from-nebula-500 to-cyan-glow shadow-lg overflow-hidden">
              <svg viewBox="0 0 64 64" fill="none" className="h-12 w-12">
                <path
                  d="M20 38c0-6 4-10 12-10 7 0 11 4 12 8.5M20 38h24M20 38c-3 0-5-2-5-5s2-5 5-5M44 38c3 0 5-2 5-5s-2-5-5-5"
                  stroke="white"
                  strokeWidth="3.2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
                <circle cx="32" cy="22" r="3.5" fill="white" />
              </svg>
            </div>
          </div>

          <div className="text-3xl font-extrabold tracking-tight text-white">
            {brand.name}
          </div>
          <div className="mt-1 text-sm text-slate-400">新一代毛玻璃风格云存储</div>

          <div className="mt-6 space-y-2 text-sm">
            <div className="flex items-center justify-center gap-2 text-slate-300">
              <span className="text-slate-400">作者:</span>
              <span className="font-medium text-white">{brand.author}</span>
            </div>
            <div className="flex items-center justify-center gap-2 text-slate-300">
              <span className="text-slate-400">版本:</span>
              <span className="rounded-full bg-white/10 px-2.5 py-0.5 font-mono text-xs text-cyan-glow border border-white/10">
                v{brand.version}
              </span>
            </div>
          </div>
        </div>

        <div className="mt-8 rounded-2xl border border-white/10 bg-white/5 p-5">
          <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-white">
            <Shield className="h-4 w-4 text-nebula-300" />
            License · 合规声明
          </div>
          <div className="space-y-3 text-xs leading-relaxed text-slate-300">
            <p>
              本项目采用{' '}
              <span className="font-semibold text-cyan-glow">GNU AGPL-3.0</span>{' '}
              开源发布，允许个人与商业用途。
            </p>
            <div className="rounded-xl border border-amber-400/30 bg-amber-400/10 px-3 py-2.5 text-amber-200/90">
              <div className="mb-1 flex items-center gap-1.5 font-semibold">
                <Info className="h-3.5 w-3.5" />
                核心义务：网络服务同样需开源
              </div>
              <div>
                AGPL-3.0 与 GPL 的唯一区别在于：若把本程序或其修改版部署到服务器供他人通过网络访问，
                <span className="font-bold text-amber-100">必须向这些使用者提供完整的对应源码</span>
                （含你的修改）；分发二进制时须随附源码或提供获取源码的书面要约，并保留{' '}
                <span className="font-semibold">LICENSE</span> 中的版权与许可声明。
              </div>
            </div>
          </div>
        </div>

        <div className="mt-5 rounded-2xl border border-white/10 bg-white/5 p-5">
          <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-white">
            <Heart className="h-4 w-4 text-rose-300" />
            致谢技术栈
          </div>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {STACK.map(({ name, Icon }) => (
              <div
                key={name}
                className="flex items-center gap-2 rounded-xl bg-white/5 px-3 py-2 border border-white/10"
              >
                <Icon className="h-3.5 w-3.5 text-cyan-glow shrink-0" />
                <span className="text-xs text-slate-200 truncate">{name}</span>
              </div>
            ))}
          </div>
        </div>

        <div className="mt-6 flex flex-col items-center gap-2 text-center text-[11px] text-slate-400">
          <div className="flex items-center gap-1.5">
            <Github className="h-3.5 w-3.5" />
            <span>Open Source · Made with {brand.name}</span>
          </div>
          <div>
            © {year} {brand.name} · Author: {brand.author} · v{brand.version}
          </div>
        </div>
      </div>
    </div>
  )
}
