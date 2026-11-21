/// <reference lib="webworker" />
// 在 Worker 里分片计算 MD5，避免大文件把主线程卡死。
// 用 SparkMD5.ArrayBuffer 的增量 append：每次只读 2MB，内存占用恒定。
import SparkMD5 from 'spark-md5'

const CHUNK_SIZE = 2 * 1024 * 1024 // 2MB

interface MD5Request {
  file: File
}

self.onmessage = (e: MessageEvent<MD5Request>) => {
  const { file } = e.data
  const spark = new SparkMD5.ArrayBuffer()
  const total = file.size
  let offset = 0

  const readNext = () => {
    if (offset >= total) {
      const hash = spark.end()
      ;(self as unknown as Worker).postMessage({ hash })
      return
    }
    const end = Math.min(offset + CHUNK_SIZE, total)
    const slice = file.slice(offset, end)
    const reader = new FileReader()
    reader.onload = (ev) => {
      const buf = ev.target?.result
      if (buf instanceof ArrayBuffer) {
        spark.append(buf)
      }
      offset = end
      // 上报进度（0-100），让 UI 显示 hashing 进度而不是卡住
      ;(self as unknown as Worker).postMessage({ progress: Math.round((offset / total) * 100) })
      readNext()
    }
    reader.onerror = () => {
      ;(self as unknown as Worker).postMessage({ error: 'read failed' })
    }
    reader.readAsArrayBuffer(slice)
  }

  readNext()
}
