// NebulaDrive 插件：backup-mirror
//
// 独立 module，不依赖宿主代码 —— 插件与宿主是两个进程，
// 共享 module 反而会造成版本耦合。只用标准库。
module github.com/nebula-drive/nebula/examples/plugins/backup-mirror

go 1.25.0
