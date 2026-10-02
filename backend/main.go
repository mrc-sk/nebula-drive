package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/controllers"
	"github.com/nebula-drive/nebula/internal/service"
	"github.com/nebula-drive/nebula/migrations"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/aria2"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
	"github.com/nebula-drive/nebula/pkg/storage"
	tlspkg "github.com/nebula-drive/nebula/pkg/tls"
	"github.com/nebula-drive/nebula/routers"
	"github.com/nebula-drive/nebula/webdav"
	"gorm.io/gorm"
)

func main() {
	dataDir := flag.String("data", "data", "数据目录")
	listen := flag.String("listen", "", "监听地址，留空则读配置")
	flag.Parse()

	conf.SetDataDir(*dataDir)

	// 启动时加载配置；未安装则保持 installed=false，由前端安装向导接管
	if err := conf.Load(); err != nil {
		log.Printf("[WARN] 加载配置失败（可能未安装）: %v", err)
	}

	// 已安装：初始化 DB、迁移、JWT、默认数据
	if conf.IsInstalled() {
		bootstrap()
	}

	addr := *listen
	if addr == "" {
		if c := conf.Current(); c != nil && c.System.Listen != "" {
			addr = c.System.Listen
		}
	}
	if addr == "" {
		addr = ":5212"
	}

	r := routers.Setup()
	routers.InitWebDAVRoutes(r)
	registerFrontend(r)
	startServer(r, addr)
}

// startServer 根据 settings tls.mode 选择 HTTP/HTTPS 启动
func startServer(r *gin.Engine, addr string) {
	tlsMode := strings.ToLower(strings.TrimSpace(readSetting("tls.mode", "off")))
	switch tlsMode {
	case "auto":
		domain := strings.TrimSpace(readSetting("tls.domain", ""))
		if domain == "" {
			if c := conf.Current(); c != nil {
				domain = c.System.TLSDomain
			}
		}
		tlsCfg, challenge, err := tlspkg.StartACME(domain)
		if err != nil {
			log.Printf("[WARN] ACME 启动失败，回退 HTTP: %v", err)
			runHTTP(r, addr)
			return
		}
		go runACMEHTTP(challenge)
		log.Printf("NebulaDrive 启动 HTTPS(ACME) 于 %s domain=%s（已安装: %v）", addr, domain, conf.IsInstalled())
		runHTTPS(r, addr, tlsCfg)
	case "manual":
		tlsCfg, err := tlspkg.LoadManual("", "")
		if err != nil {
			log.Printf("[WARN] 手动证书加载失败，回退 HTTP: %v", err)
			runHTTP(r, addr)
			return
		}
		log.Printf("NebulaDrive 启动 HTTPS(manual) 于 %s（已安装: %v）", addr, conf.IsInstalled())
		runHTTPS(r, addr, tlsCfg)
	default:
		runHTTP(r, addr)
	}
}

func runHTTP(r *gin.Engine, addr string) {
	log.Printf("NebulaDrive 启动 HTTP 于 %s（已安装: %v）", addr, conf.IsInstalled())
	if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("启动失败: %v", err)
	}
}

func runHTTPS(r *gin.Engine, addr string, tlsCfg *tls.Config) {
	srv := &http.Server{Addr: addr, Handler: r, TLSConfig: tlsCfg}
	if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		log.Fatalf("启动失败: %v", err)
	}
}

// runACMEHTTP 80 端口处理 ACME challenge（同时 Let's Encrypt HTTP-01 校验）
func runACMEHTTP(h http.Handler) {
	mux := http.NewServeMux()
	if h != nil {
		mux.Handle("/", h)
	}
	if err := http.ListenAndServe(":80", mux); err != nil {
		log.Printf("[WARN] ACME 80 端口监听失败: %v", err)
	}
}

func readSetting(key, def string) string {
	if db.Get() == nil {
		return def
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}

// bootstrap 已安装时初始化运行时依赖
func bootstrap() {
	cfg := conf.Current()
	if cfg == nil {
		return
	}
	if err := db.Init(&cfg.DB); err != nil {
		log.Printf("[WARN] DB 初始化失败: %v", err)
		return
	}
	if err := models.AutoMigrate(); err != nil {
		log.Printf("[WARN] 数据库迁移失败: %v", err)
	}
	// 回填遗留 FileObject 引用计数（V1-0.0.1 升级用户秒传/复制共享物理文件，没有 FileObject）
	storage.BackfillFileObjects()
	// 存量敏感字段（2FA secret / OAuth ClientSecret / 存储配置 / 访问令牌）明文 → 加密或哈希。
	// 幂等；失败不阻塞启动（认证路径另有明文兜底）。
	models.MigrateSensitiveFields()
	// 注入 internal/service 的实现：
	//   - 设置读取：让 SSRF/白名单/上传上限等配置能读到 DB 里的 settings
	//   - 配额解析：让 service 层的原子配额占用（AddStorageTx）能算出套餐/用户组额度
	// 不做这步，service 会退回各自的兜底实现（仍可工作，但读不到用户自定义设置）。
	service.SetSettingGetter(func(key string) (string, bool) {
		if db.Get() == nil {
			return "", false
		}
		var s models.Setting
		if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
			return "", false
		}
		return s.Value, true
	})
	service.SetQuotaProvider(func(tx *gorm.DB, userID uint) (int64, string) {
		var u models.User
		if err := tx.Select("plan_id", "plan_expire_at", "group_id").First(&u, userID).Error; err != nil {
			return -1, "unknown"
		}
		// 套餐优先（且在有效期内）
		if u.PlanID > 0 && u.PlanExpireAt != nil && u.PlanExpireAt.After(time.Now()) {
			var p models.Plan
			if tx.First(&p, u.PlanID).Error == nil {
				return p.MaxStorage, "plan:" + p.Name
			}
		}
		var g models.Group
		if tx.First(&g, u.GroupID).Error == nil {
			return g.MaxStorage, "group:" + g.Name
		}
		return -1, "fallback"
	})
	// 初始化默认套餐（Ultra/Pro/Pro Max）
	controllers.SeedDefaultPlans()
	if err := migrations.Run(db.Get()); err != nil {
		log.Printf("[WARN] 版本化迁移失败: %v", err)
	}
	if err := jwt.Init(); err != nil {
		log.Printf("[WARN] JWT 初始化失败: %v", err)
	}
	ensureDefaults(cfg.System.UploadPath)

	// 尝试内嵌启动 aria2，失败静默（系统未安装 aria2 时回退到内置 HTTP 下载）
	am := aria2.GetManager()
	if err := am.StartEmbedded(); err != nil {
		log.Printf("[INFO] aria2 未启用: %v", err)
	}

	// 启动回收站自动清理 goroutine（每 6 小时扫描一次）
	controllers.StartTrashCleanup()
	// 启动 WebDAV 过期锁回收（LOCK 现在会真实记录锁，必须有对应回收）
	webdav.StartLockGC()
}

// ensureDefaults 确保默认用户组与存储策略存在（幂等）
func ensureDefaults(uploadPath string) {
	if uploadPath == "" {
		uploadPath = "uploads"
	}
	var g models.Group
	if err := db.Get().First(&g, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Group{ID: 1, Name: "default", MaxStorage: 10 * 1024 * 1024 * 1024, ShareEnabled: true, WebDAVEnabled: true})
	}
	var g2 models.Group
	if err := db.Get().First(&g2, 2).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Group{ID: 2, Name: "admin", MaxStorage: -1})
	}
	var p models.Policy
	if err := db.Get().First(&p, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Policy{ID: 1, Name: "本地存储", Type: "local", Config: models.From(`{"path":"` + uploadPath + `"}`), IsDefault: true})
	}
	ensureBrandSettings()
}

func upsertSetting(key, value string) {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Setting{Key: key, Value: value})
	} else if s.Value == "" {
		db.Get().Model(&s).Update("value", value)
	}
}

// ensureBrandSettings 写入合规品牌默认设置（幂等）
func ensureBrandSettings() {
	upsertSetting("brand.name", "NebulaDrive")
	upsertSetting("brand.author", "NebulaDrive Team")
	upsertSetting("brand.version", "1.0.0")
	upsertSetting("trash.retention_days", "30")
	upsertSetting("file.max_versions", "10")
	// TLS
	upsertSetting("tls.mode", "off")
	// 上传安全
	upsertSetting("upload.allowed_extensions", "jpg,jpeg,png,gif,webp,mp4,webm,mp3,wav,pdf,doc,docx,xls,xlsx,ppt,pptx,zip,rar,7z,tar,gz,txt,md,go,py,js,ts,json,yaml,yml,xml,csv")
	upsertSetting("upload.enable_magic_check", "true")
	// 密码强度
	upsertSetting("security.password_min_length", "8")
	upsertSetting("security.password_require_upper", "true")
	upsertSetting("security.password_require_digit", "true")
	upsertSetting("security.password_require_special", "false")
	upsertSetting("security.captcha_enabled", "false")
	upsertSetting("security.allow_register", "false")
}
