package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	tencentcos "github.com/tencentyun/cos-go-sdk-v5"
)

type cosConfig struct {
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	SecretID  string `json:"secretId"`
	SecretKey string `json:"secretKey"`
	Scheme    string `json:"scheme"`
}

type COSHandler struct {
	cfg    cosConfig
	client *tencentcos.Client
}

func newCOSHandler(cfgJSON string) (Handler, error) {
	var cfg cosConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, errors.New("cos policy config invalid json: " + err.Error())
	}
	if cfg.Region == "" {
		return nil, errors.New("cos policy config missing key: region")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("cos policy config missing key: bucket")
	}
	if cfg.SecretID == "" {
		return nil, errors.New("cos policy config missing key: secretId")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("cos policy config missing key: secretKey")
	}
	scheme := cfg.Scheme
	if scheme == "" {
		scheme = "https"
	}
	u, _ := url.Parse(fmt.Sprintf("%s://%s.cos.%s.myqcloud.com", scheme, cfg.Bucket, cfg.Region))
	b := &tencentcos.BaseURL{BucketURL: u}
	client := tencentcos.NewClient(b, &http.Client{
		Transport: &tencentcos.AuthorizationTransport{
			SecretID:  cfg.SecretID,
			SecretKey: cfg.SecretKey,
		},
	})
	return &COSHandler{cfg: cfg, client: client}, nil
}

func cosUploadConfig(size int64) (partSize int64, threadPoolSize int) {
	partSize = 10 * 1024 * 1024
	threadPoolSize = 5
	if size > 5*1024*1024*1024 {
		partSize = 20 * 1024 * 1024
		threadPoolSize = 16
	}
	return partSize, threadPoolSize
}

func (h *COSHandler) Put(src io.Reader, name string, size int64) error {
	ctx := context.Background()
	if size < 100*1024*1024 {
		_, err := h.client.Object.Put(ctx, name, src, nil)
		return err
	}
	partSize, threadPoolSize := cosUploadConfig(size)
	tmpFile, err := os.CreateTemp("", "cos-upload-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	_, err = io.Copy(tmpFile, src)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)
	opt := &tencentcos.MultiUploadOptions{
		OptIni:         nil,
		PartSize:       partSize,
		ThreadPoolSize: threadPoolSize,
		CheckPoint:     false,
	}
	_, _, err = h.client.Object.Upload(ctx, name, tmpPath, opt)
	return err
}

func (h *COSHandler) Get(name string) (io.ReadCloser, error) {
	ctx := context.Background()
	resp, err := h.client.Object.Get(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (h *COSHandler) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	ctx := context.Background()
	var rng string
	if length <= 0 {
		rng = fmt.Sprintf("bytes=%d-", offset)
	} else {
		rng = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}
	opt := &tencentcos.ObjectGetOptions{
		Range: rng,
	}
	resp, err := h.client.Object.Get(ctx, name, opt)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (h *COSHandler) Delete(name string) error {
	ctx := context.Background()
	_, err := h.client.Object.Delete(ctx, name)
	return err
}

func (h *COSHandler) Size(name string) (int64, error) {
	ctx := context.Background()
	resp, err := h.client.Object.Head(ctx, name, nil)
	if err != nil {
		return 0, err
	}
	return strconvInt64(resp.Header.Get("Content-Length"))
}

func (h *COSHandler) PresignGet(name string, expires time.Duration) (string, error) {
	ctx := context.Background()
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	up, err := h.client.Object.GetPresignedURL(ctx, http.MethodGet, name, h.cfg.SecretID, h.cfg.SecretKey, expires, nil)
	if err != nil {
		return "", err
	}
	return up.String(), nil
}

// Copy 通过 COS Object.Copy 在同 bucket 内复制对象
func (h *COSHandler) Copy(src, dst string) error {
	ctx := context.Background()
	source := fmt.Sprintf("%s.cos.%s.myqcloud.com/%s", h.cfg.Bucket, h.cfg.Region, src)
	_, _, err := h.client.Object.Copy(ctx, dst, source, nil)
	return err
}
