package filesystem

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	alioss "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// ThumbHandler 缩略图接口
type ThumbHandler interface {
	GetThumb(name string, width, height int) (io.ReadCloser, error)
}

// thumbsRoot 本地缩略图缓存目录
func thumbsRoot() string {
	return "data/thumbs"
}

// sanitizeThumbName 把 sourceName 转成可做文件名的 key
func sanitizeThumbName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
}

// GetThumb 本地实现：命中缓存则返回，否则返回原图（imaging 库未集成）
func (l *Local) GetThumb(name string, width, height int) (io.ReadCloser, error) {
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}
	cachePath := filepath.Join(thumbsRoot(), fmt.Sprintf("%s_%dx%d.jpg", sanitizeThumbName(name), width, height))
	if f, err := os.Open(cachePath); err == nil {
		return f, nil
	}
	// imaging 不可用，回退返回原图
	return l.Get(name)
}

// GetThumb S3 无原生图片处理，返回原图
func (h *S3Handler) GetThumb(name string, width, height int) (io.ReadCloser, error) {
	return h.Get(name)
}

// GetThumb OSS 用 ?x-oss-process=image/resize 图片处理参数
func (h *OSSHandler) GetThumb(name string, width, height int) (io.ReadCloser, error) {
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}
	process := fmt.Sprintf("image/resize,w_%d,h_%d", width, height)
	return h.bucket.GetObject(name, alioss.Process(process))
}

// GetThumb COS 用 ?imageMogr2/thumbnail/{w}x{h} 图片处理参数；失败回退原图
func (h *COSHandler) GetThumb(name string, width, height int) (io.ReadCloser, error) {
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 300
	}
	signedURL, err := h.PresignGet(name, 5*time.Minute)
	if err == nil && signedURL != "" {
		sep := "?"
		if strings.Contains(signedURL, "?") {
			sep = "&"
		}
		processedURL := signedURL + sep + fmt.Sprintf("imageMogr2/thumbnail/%dx%d", width, height)
		resp, gerr := http.Get(processedURL)
		if gerr == nil && resp.StatusCode < 400 {
			return resp.Body, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	// 回退原图
	return h.Get(name)
}
