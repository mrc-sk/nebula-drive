import { X } from 'lucide-react'
import type { ReactNode } from 'react'

/**
 * 通用模态框。
 *
 * 此前项目里有两份重复实现（Files.tsx 私有一份、pages/admin/Users.tsx 导出一份），
 * 新页面再各自复制会让样式继续漂移 —— 统一收敛到这里。
 *
 * 与旧版的差异：旧 Files.tsx 那份点击遮罩会因 stopPropagation 缺失而导致内部点击
 * 误关，这里显式阻止冒泡。
 */
export function Modal({
  title,
  onClose,
  children,
  wide,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  wide?: boolean
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-fade-in"
      onClick={onClose}
    >
      <div
        className={`glass-strong animate-slide-up w-full overflow-hidden rounded-2xl ${wide ? 'max-w-4xl' : 'max-w-md'}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-3.5">
          <h3 className="text-sm font-semibold">{title}</h3>
          <button
            type="button"
            onClick={onClose}
            className="grid h-7 w-7 place-items-center rounded-lg text-slate-400 transition hover:bg-white/10 hover:text-slate-100"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  )
}
