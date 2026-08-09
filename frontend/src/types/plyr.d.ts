declare module 'plyr' {
  export interface PlyrSource {
    src: string
    type?: string
    size?: string
  }
  export interface PlyrTrack {
    kind: string
    label?: string
    srclang?: string
    src: string
    default?: boolean
  }
  export interface PlyrVideoOptions {
    src?: string | PlyrSource[]
    type?: string
    title?: string
    poster?: string
    tracks?: PlyrTrack[]
    [key: string]: any
  }
  export interface PlyrOptions {
    enabled?: boolean
    controls?: string[]
    settings?: string[]
    speed?: { selected?: number; options?: number[] }
    quality?: { default?: number; options?: number[]; forced?: boolean; onChange?: (q: number) => void }
    loop?: { active?: boolean }
    keyboard?: { focused?: boolean; global?: boolean }
    tooltips?: { controls?: boolean; seek?: boolean }
    fullscreen?: { enabled?: boolean; fallback?: boolean; iosNative?: boolean }
    ratio?: string
    storage?: { enabled?: boolean; key?: string }
    pip?: boolean
    autopause?: boolean
    seekTime?: number
    toggleInvert?: boolean
    [key: string]: any
  }
  export default class Plyr {
    constructor(target: string | HTMLElement | NodeList, options?: PlyrOptions & PlyrVideoOptions)
    static setup(target: string | HTMLElement | NodeList, options?: PlyrOptions & PlyrVideoOptions): Plyr[]
    destroy(): void
    play(): Promise<void>
    pause(): void
    togglePlay(toggle?: boolean): void
    stop(): void
    seek(time: number): void
    forward(seekTime?: number): void
    rewind(seekTime?: number): void
    on(type: string, listener: (event?: any) => void): void
    once(type: string, listener: (event?: any) => void): void
    off(type: string, listener?: (event?: any) => void): void
    fullscreen: { enter: () => Promise<void>; exit: () => Promise<void>; toggle: () => Promise<void>; isActive: () => boolean }
    [key: string]: any
  }
}
