import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Users, FileText, Share2, HardDrive, Loader2 } from 'lucide-react'
import ReactECharts from 'echarts-for-react'
import type { EChartsOption } from 'echarts'
import { api } from '../../api/client'

type TrafficRange = 7 | 30 | 365

/* ---------- mock 数据生成（后端 API 缺失时使用） ---------- */
function mockTraffic(days: number) {
  const out: { date: string; upload: number; download: number }[] = []
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    out.push({
      date: `${d.getMonth() + 1}/${d.getDate()}`,
      upload: Math.round(50 + Math.random() * 950),
      download: Math.round(80 + Math.random() * 1200),
    })
  }
  return out
}

function mockFileTypes() {
  return [
    { name: 'image', value: Math.round(200 + Math.random() * 400) },
    { name: 'video', value: Math.round(80 + Math.random() * 200) },
    { name: 'audio', value: Math.round(40 + Math.random() * 120) },
    { name: 'document', value: Math.round(300 + Math.random() * 500) },
    { name: 'archive', value: Math.round(60 + Math.random() * 180) },
    { name: 'other', value: Math.round(120 + Math.random() * 260) },
  ]
}

function mockActivity(days: number) {
  const out: { date: string; count: number }[] = []
  const today = new Date()
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    const y = d.getFullYear()
    const m = String(d.getMonth() + 1).padStart(2, '0')
    const da = String(d.getDate()).padStart(2, '0')
    const r = Math.random()
    out.push({
      date: `${y}-${m}-${da}`,
      count: r < 0.45 ? 0 : Math.floor(r * 24),
    })
  }
  return out
}

/* ---------- 工具 ---------- */
function formatSize(b: number) {
  if (!b) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = Number(b) || 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}

function formatDate(s: string) {
  if (!s) return '-'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const da = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${da}`
}

const TYPE_LABELS: Record<string, string> = {
  image: 'image',
  video: 'video',
  audio: 'audio',
  document: 'document',
  archive: 'archive',
  other: 'other',
}

export default function Dashboard() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [data, setData] = useState<any>({
    totalUsers: 0,
    totalFiles: 0,
    totalShares: 0,
    storageUsed: 0,
    recentUsers: [],
    recentFiles: [],
  })

  // 图表数据
  const [trafficRange, setTrafficRange] = useState<TrafficRange>(7)
  const [traffic, setTraffic] = useState<{ date: string; upload: number; download: number }[]>([])
  const [fileTypes, setFileTypes] = useState<{ name: string; value: number }[]>([])
  const [activity, setActivity] = useState<{ date: string; count: number }[]>([])

  useEffect(() => {
    ;(async () => {
      try {
        const res = await api.admin.dashboard()
        if (res?.code === 0 && res.data) {
          setData(res.data)
        }
      } catch {
        // ignore
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  // 上传/下载趋势
  useEffect(() => {
    let alive = true
    ;(async () => {
      let rows: { date: string; upload: number; download: number }[] = []
      try {
        const res = await api.admin.trafficStats(trafficRange)
        if (alive && res?.code === 0 && Array.isArray(res.data) && res.data.length > 0) {
          rows = res.data.map((d: any) => ({
            date: d.date || d.day || '',
            upload: Number(d.upload ?? d.uploadBytes ?? d.uploadMB ?? 0),
            download: Number(d.download ?? d.downloadBytes ?? d.downloadMB ?? 0),
          }))
        }
      } catch {
        // 后端无 API
      }
      if (alive && rows.length === 0) rows = mockTraffic(trafficRange)
      if (alive) setTraffic(rows)
    })()
    return () => {
      alive = false
    }
  }, [trafficRange])

  // 文件类型分布
  useEffect(() => {
    let alive = true
    ;(async () => {
      let rows: { name: string; value: number }[] = []
      try {
        const res = await api.admin.fileTypeStats()
        if (alive && res?.code === 0 && Array.isArray(res.data) && res.data.length > 0) {
          rows = res.data.map((d: any) => ({
            name: d.name || d.type || 'other',
            value: Number(d.value ?? d.count ?? 0),
          }))
        }
      } catch {
        // ignore
      }
      if (alive && rows.length === 0) rows = mockFileTypes()
      if (alive) setFileTypes(rows)
    })()
    return () => {
      alive = false
    }
  }, [])

  // 用户活跃度热力图
  useEffect(() => {
    let alive = true
    ;(async () => {
      let rows: { date: string; count: number }[] = []
      try {
        const res = await api.admin.activityStats(365)
        if (alive && res?.code === 0 && Array.isArray(res.data) && res.data.length > 0) {
          rows = res.data.map((d: any) => ({
            date: d.date || d.day || '',
            count: Number(d.count ?? d.value ?? 0),
          }))
        }
      } catch {
        // ignore
      }
      if (alive && rows.length === 0) rows = mockActivity(365)
      if (alive) setActivity(rows)
    })()
    return () => {
      alive = false
    }
  }, [])

  const cards = [
    { label: t('ns_admin.totalUsers'), value: data.totalUsers, Icon: Users, color: 'from-indigo-500 to-sky-500' },
    { label: t('ns_admin.totalFiles'), value: data.totalFiles, Icon: FileText, color: 'from-cyan-500 to-emerald-500' },
    { label: t('ns_admin.totalShares'), value: data.totalShares, Icon: Share2, color: 'from-amber-500 to-orange-500' },
    { label: t('ns_admin.storageUsed'), value: formatSize(data.storageUsed), Icon: HardDrive, color: 'from-purple-500 to-pink-500' },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('ns_admin.dashboard')}</h1>
        <p className="mt-1 text-sm text-slate-400">NebulaDrive overview</p>
      </div>

      {loading ? (
        <div className="grid h-48 place-items-center">
          <Loader2 className="h-8 w-8 animate-spin text-nebula-300" />
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {cards.map((c) => (
              <div key={c.label} className="glass rounded-2xl p-5">
                <div className="flex items-start justify-between">
                  <div>
                    <div className="text-sm text-slate-400">{c.label}</div>
                    <div className="mt-2 text-2xl font-semibold">{c.value ?? '-'}</div>
                  </div>
                  <div className={`grid h-11 w-11 place-items-center rounded-xl bg-gradient-to-br ${c.color} shadow-lg`}>
                    <c.Icon className="h-5 w-5 text-white" />
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* 三图表：2×2 网格，第一个跨2列 */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="glass rounded-2xl p-5 lg:col-span-2">
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <h3 className="font-semibold">{t('trafficStats')}</h3>
                <div className="flex items-center gap-1 rounded-xl bg-white/5 p-1">
                  {([7, 30, 365] as const).map((d) => (
                    <button
                      key={d}
                      type="button"
                      onClick={() => setTrafficRange(d)}
                      className={`rounded-lg px-2.5 py-1 text-xs transition ${
                        trafficRange === d ? 'bg-nebula-500/40 text-white' : 'text-slate-300 hover:bg-white/10'
                      }`}
                    >
                      {d === 7 ? t('last7Days') : d === 30 ? t('last30Days') : t('last365Days')}
                    </button>
                  ))}
                </div>
              </div>
              <TrafficChart data={traffic} />
            </div>

            <div className="glass rounded-2xl p-5">
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-semibold">{t('fileTypes')}</h3>
              </div>
              <FileTypesChart data={fileTypes} />
            </div>

            <div className="glass rounded-2xl p-5">
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-semibold">{t('userActivity')}</h3>
              </div>
              <ActivityHeatmap data={activity} />
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="glass rounded-2xl p-5">
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-semibold">{t('ns_admin.recentUsers')}</h3>
              </div>
              <div className="space-y-2">
                {(data.recentUsers || []).slice(0, 8).map((u: any) => (
                  <div key={u.id} className="flex items-center gap-3 rounded-xl p-2 hover:bg-white/5">
                    <span className="grid h-9 w-9 place-items-center rounded-full bg-gradient-to-br from-indigo-500 to-cyan-400 text-sm font-semibold text-white">
                      {(u.userName || u.nickname || 'A').charAt(0).toUpperCase()}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{u.userName}</div>
                      <div className="truncate text-xs text-slate-400">{u.email || '-'}</div>
                    </div>
                    <div className="text-xs text-slate-400">{formatDate(u.createdAt)}</div>
                  </div>
                ))}
                {(!data.recentUsers || data.recentUsers.length === 0) && (
                  <div className="py-6 text-center text-sm text-slate-400">-</div>
                )}
              </div>
            </div>

            <div className="glass rounded-2xl p-5">
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-semibold">{t('ns_admin.recentFiles')}</h3>
              </div>
              <div className="space-y-2">
                {(data.recentFiles || []).slice(0, 8).map((f: any) => (
                  <div key={f.id} className="flex items-center gap-3 rounded-xl p-2 hover:bg-white/5">
                    <FileText className="h-4 w-4 text-sky-400" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{f.name}</div>
                      <div className="truncate text-xs text-slate-400">{formatSize(f.size)}</div>
                    </div>
                    <div className="text-xs text-slate-400">{formatDate(f.createdAt)}</div>
                  </div>
                ))}
                {(!data.recentFiles || data.recentFiles.length === 0) && (
                  <div className="py-6 text-center text-sm text-slate-400">-</div>
                )}
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  )
}

/* ============================== 上传/下载趋势折线图 ============================== */
function TrafficChart({ data }: { data: { date: string; upload: number; download: number }[] }) {
  const { t } = useTranslation()
  const option = useMemo<EChartsOption>(() => {
    return {
      tooltip: {
        trigger: 'axis',
        backgroundColor: 'rgba(20,24,50,0.92)',
        borderColor: 'rgba(255,255,255,0.15)',
        textStyle: { color: '#e2e8f0' },
      },
      legend: {
        data: [t('uploadVolume'), t('downloadVolume')],
        textStyle: { color: '#cbd5e1' },
        top: 0,
      },
      grid: { left: 50, right: 60, top: 40, bottom: 30 },
      xAxis: {
        type: 'category',
        data: data.map((d) => d.date),
        axisLine: { lineStyle: { color: 'rgba(255,255,255,0.2)' } },
        axisLabel: { color: '#94a3b8', fontSize: 10 },
      },
      yAxis: [
        {
          type: 'value',
          name: t('uploadVolume') + '(MB)',
          nameTextStyle: { color: '#60a5fa', fontSize: 11 },
          axisLine: { show: true, lineStyle: { color: 'rgba(96,165,250,0.5)' } },
          axisLabel: { color: '#94a3b8', fontSize: 10 },
          splitLine: { lineStyle: { color: 'rgba(255,255,255,0.08)' } },
        },
        {
          type: 'value',
          name: t('downloadVolume') + '(MB)',
          nameTextStyle: { color: '#34d399', fontSize: 11 },
          axisLine: { show: true, lineStyle: { color: 'rgba(52,211,153,0.5)' } },
          axisLabel: { color: '#94a3b8', fontSize: 10 },
          splitLine: { show: false },
        },
      ],
      series: [
        {
          name: t('uploadVolume'),
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: data.map((d) => d.upload),
          lineStyle: { color: '#3b82f6', width: 2 },
          itemStyle: { color: '#3b82f6' },
          areaStyle: {
            color: {
              type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
              colorStops: [
                { offset: 0, color: 'rgba(59,130,246,0.35)' },
                { offset: 1, color: 'rgba(59,130,246,0.02)' },
              ],
            },
          },
        },
        {
          name: t('downloadVolume'),
          type: 'line',
          yAxisIndex: 1,
          smooth: true,
          showSymbol: false,
          data: data.map((d) => d.download),
          lineStyle: { color: '#10b981', width: 2 },
          itemStyle: { color: '#10b981' },
          areaStyle: {
            color: {
              type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
              colorStops: [
                { offset: 0, color: 'rgba(16,185,129,0.35)' },
                { offset: 1, color: 'rgba(16,185,129,0.02)' },
              ],
            },
          },
        },
      ],
    }
  }, [data, t])
  return <ReactECharts option={option} style={{ height: 280 }} notMerge />
}

/* ============================== 文件类型分布饼图 ============================== */
function FileTypesChart({ data }: { data: { name: string; value: number }[] }) {
  const { t } = useTranslation()
  const palette = ['#22d3ee', '#f97316', '#facc15', '#60a5fa', '#a855f7', '#94a3b8']
  const option = useMemo<EChartsOption>(() => {
    return {
      tooltip: {
        trigger: 'item',
        backgroundColor: 'rgba(20,24,50,0.92)',
        borderColor: 'rgba(255,255,255,0.15)',
        textStyle: { color: '#e2e8f0' },
        formatter: '{b}: {c} ({d}%)',
      },
      legend: {
        bottom: 0,
        textStyle: { color: '#cbd5e1', fontSize: 11 },
        type: 'scroll',
      },
      series: [
        {
          name: t('fileTypes'),
          type: 'pie',
          radius: ['40%', '68%'],
          center: ['50%', '44%'],
          avoidLabelOverlap: true,
          selectedMode: 'single',
          itemStyle: {
            borderColor: 'rgba(15,23,42,0.6)',
            borderWidth: 2,
            borderRadius: 6,
          },
          label: {
            show: true,
            color: '#e2e8f0',
            fontSize: 11,
            formatter: '{b}\n{d}%',
          },
          labelLine: { lineStyle: { color: 'rgba(255,255,255,0.35)' } },
          emphasis: {
            label: { show: true, fontSize: 13, fontWeight: 'bold' },
            itemStyle: { shadowBlur: 16, shadowColor: 'rgba(34,211,238,0.4)' },
          },
          data: data.map((d, i) => ({
            name: t(TYPE_LABELS[d.name] || d.name) || d.name,
            value: d.value,
            itemStyle: { color: palette[i % palette.length] },
          })),
        },
      ],
    }
  }, [data, t])
  return <ReactECharts option={option} style={{ height: 280 }} notMerge />
}

/* ============================== 用户活跃度热力图 ============================== */
function ActivityHeatmap({ data }: { data: { date: string; count: number }[] }) {
  const { t } = useTranslation()
  const option = useMemo<EChartsOption>(() => {
    const cells = data.map((d) => [d.date, d.count])
    const range =
      data.length >= 2 ? [data[0].date, data[data.length - 1].date] : String(new Date().getFullYear())
    const max = Math.max(1, ...data.map((d) => d.count))
    return {
      tooltip: {
        backgroundColor: 'rgba(20,24,50,0.92)',
        borderColor: 'rgba(255,255,255,0.15)',
        textStyle: { color: '#e2e8f0' },
        formatter: (p: any) => {
          if (!p || !p.value || p.value.length < 2) return ''
          return `${p.value[0]}<br/>${t('userActivity')}: ${p.value[1]}`
        },
      },
      visualMap: {
        min: 0,
        max,
        show: false,
        inRange: { color: ['#1e293b', '#0e7490', '#22d3ee', '#34d399', '#facc15', '#f97316'] },
      },
      calendar: {
        top: 30,
        left: 30,
        right: 16,
        cellSize: ['auto', 13],
        range: range as any,
        itemStyle: {
          borderWidth: 2,
          borderColor: 'rgba(15,23,42,0.6)',
          color: 'rgba(255,255,255,0.04)',
        },
        splitLine: { show: false },
        yearLabel: { show: false },
        monthLabel: { color: '#94a3b8', fontSize: 10 },
        dayLabel: { color: '#94a3b8', fontSize: 10 },
      },
      series: [
        {
          type: 'heatmap',
          coordinateSystem: 'calendar',
          data: cells,
          itemStyle: { borderRadius: 2 },
        },
      ],
    }
  }, [data, t])
  return (
    <div className="overflow-x-auto">
      <ReactECharts option={option} style={{ height: 200, minWidth: 560 }} notMerge />
    </div>
  )
}
