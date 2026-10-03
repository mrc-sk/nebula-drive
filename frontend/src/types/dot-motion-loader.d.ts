import type React from 'react'

// 点阵加载动画 Web Component（public/dot-motion-loader.js）的 JSX 类型声明。
// 该组件为自包含 IIFE，挂载时通过 customElements.define 自动注册，
// 支持 style / paused / speed 等标准 HTML 属性。
declare global {
  namespace JSX {
    interface IntrinsicElements {
      'dot-motion-loader': React.DetailedHTMLProps<
        React.HTMLAttributes<HTMLElement>,
        HTMLElement
      >
    }
  }
}
