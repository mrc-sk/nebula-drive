# Linux 版怎么跑起来

这份是给**第一次拿到这个包、没看过完整文档**的 Linux 用户看的。
只讲 Linux 相关的事；功能介绍、常见问题看包里的 `试用说明.md`。

---

## 一、三步跑起来

**第1 步：解压**

```bash
unzip NebulaDrive-V26-10.0-b-linux-amd64.zip
cd NebulaDrive-V26-10.0-b-linux-amd64
```

> 如果习惯用图形界面解压（右键 → 解压到此处），也行。
> **重点是一定要「解压」，不要直接在压缩包里双击运行** —— Linux 不能那样执行。

**第 2 步：给权限**

包里的文件已经带好了权限，**但有些解压工具会丢掉它**。
如果直接跑提示权限不足，执行一次：

```bash
chmod +x nebula start.sh stop.sh
```

**第 3 步：启动**

```bash
./start.sh
```

然后浏览器打开它显示的地址（默认 `http://127.0.0.1:5212`）。

第一次打开会让你走一个安装向导，5 步全部保持默认，最后设个管理员账号就行。
数据库选**SQLite**（默认就是），不需要装 MySQL。

---

## 二、停止服务

| 你怎么启动的 | 怎么停 |
|---|---|
| `./start.sh`（前台） | 在那个终端按 `Ctrl+C` |
| `./start.sh --daemon`（后台） | `./stop.sh` |
| 后台但找不到 stop.sh | `kill \$(cat .nebula.pid)` |

后台运行日志在同目录的 `nebula.log`：

```bash
tail -f nebula.log
```

---

## 三、这个包不需要什么

**不需要**装 Go、Node.js、MySQL、Java、Docker。解压就能跑。

技术上：Go 编译时设了 `CGO_ENABLED=0`，所以是**纯静态链接**，
不依赖 glibc —— 在CentOS 7 / Ubuntu 16.04 这类老发行版上同样能跑，
不用折腾装新系统。

也不需要额外装 aria2。只是**离线下载（磁力/BT）**功能在没装 aria2c 时不可用，
HTTP 直链下载会自动回退到内置方式。其余功能不受影响。

---

## 四、Linux 常见的几个坑

### 提示 `bad interpreter: /bin/sh^M`

脚本是 CRLF 行尾导致的。但这个包里的脚本已经处理成 LF 了，
出现这个错说明**下载/解压过程中文件被改坏了**。重新下载一次即可。

### 提示 `./start.sh: Permission denied`

见上面「第 2 步」，`chmod +x` 一次就好。

### 提示 `Exec format error`

下载错了包 —— 拿了 Windows 或 macOS 版。
Linux 的包文件名里是 `linux-amd64`。

**或者你的机器是 arm64**（比如树莓派、部分国产笔记本）。
这个包是 x86_64 的，在 arm64 上跑不了，需要另要一个 arm64 版本。

自查：

```bash
uname -m
```

输出 `x86_64` 或 `amd64` → 这个包能用。
输出 `aarch64` 或 `arm64` → 换 arm64 版本。

### 端口被占用 / 启动后地址不对

脚本会自动避开占用，从 5212 开始往后找（5213、5214…）。
**以窗口里实际打印的地址为准**，别记成固定的 5212。

### 防火墙 / 只能本机访问

这是**故意设计**：这个版本只监听 `127.0.0.1`，也就是只有本机能访问，
不给局域网和公网暴露（原因见 `试用说明.md` 的「部署形态」一节）。

想在局域网里用，需要自己改监听地址：

```bash
./nebula -listen 0.0.0.0:5212
```

⚠️ 改之前想清楚：这个版本是单实例设计，且默认没有强制 HTTPS，
直接暴露到公网风险不小。放到公网建议前面套一层 Nginx + HTTPS + 反代。

### 图形界面 / 桌面图标

没有提供。启动后用浏览器访问命令行里显示的地址就行。

想要开机自启，可以搜「systemd user service」怎么写，
或者直接用 crontab 的 `@reboot`。

---

## 五、数据在哪、怎么备份

数据全在**同目录的 `data` 文件夹**，就 5 个文件，不会散落。

备份就是把整个 `data` 目录复制走：

```bash
cp -r data data-backup
```

⚠️ 里面有 `secret.key` —— 这是加密密钥，**丢了里面的加密数据就解不开了**，
备份时别漏。

想彻底重来：停掉服务，删掉 `data` 目录，重新 `./start.sh` 回到安装向导
（会丢数据）。

---

## 六、出问题怎么反馈

卡住了按这个顺序看：

1. **把报错贴出来** —— 在终端里跑 `./start.sh`（不要 `./start.sh --daemon`），
   报错直接显示在窗口里
2. **看日志** —— 后台运行的话 `tail -50 nebula.log`
3. **GitHub Issues**：https://github.com/mrc-sk/nebula-drive/issues
   （描述里写清楚发行版：`cat /etc/os-release` 的输出）

协议：AGPL-3.0　源码：https://github.com/mrc-sk/nebula-drive
