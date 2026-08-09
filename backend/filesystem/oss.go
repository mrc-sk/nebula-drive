package filesystem

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	alioss "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type ossConfig struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	Cname           bool   `json:"cname"`
}

type OSSHandler struct {
	cfg    ossConfig
	client *alioss.Client
	bucket *alioss.Bucket
}

func newOSSHandler(cfgJSON string) (Handler, error) {
	var cfg ossConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, errors.New("oss policy config invalid json: " + err.Error())
	}
	if cfg.Endpoint == "" {
		return nil, errors.New("oss policy config missing key: endpoint")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("oss policy config missing key: bucket")
	}
	if cfg.AccessKeyID == "" {
		return nil, errors.New("oss policy config missing key: accessKeyId")
	}
	if cfg.AccessKeySecret == "" {
		return nil, errors.New("oss policy config missing key: accessKeySecret")
	}
	var opts []alioss.ClientOption
	if cfg.Cname {
		opts = append(opts, alioss.UseCname(cfg.Cname))
	}
	client, err := alioss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret, opts...)
	if err != nil {
		return nil, err
	}
	bucket, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, err
	}
	return &OSSHandler{cfg: cfg, client: client, bucket: bucket}, nil
}

func ossUploadConfig(size int64) (partSize int64, concurrency int) {
	partSize = 10 * 1024 * 1024
	concurrency = 5
	if size > 5*1024*1024*1024 {
		partSize = 20 * 1024 * 1024
		concurrency = 16
	}
	return partSize, concurrency
}

func (h *OSSHandler) Put(src io.Reader, name string, size int64) error {
	if size < 100*1024*1024 {
		return h.bucket.PutObject(name, src)
	}
	partSize, concurrency := ossUploadConfig(size)
	tmpFile, err := os.CreateTemp("", "oss-upload-*")
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
	return h.bucket.UploadFile(name, tmpPath, partSize, alioss.Routines(concurrency))
}

func (h *OSSHandler) Get(name string) (io.ReadCloser, error) {
	return h.bucket.GetObject(name)
}

func (h *OSSHandler) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	var opts []alioss.Option
	if length <= 0 {
		opts = append(opts, alioss.Range(offset, -1))
	} else {
		opts = append(opts, alioss.Range(offset, offset+length-1))
	}
	return h.bucket.GetObject(name, opts...)
}

func (h *OSSHandler) Delete(name string) error {
	return h.bucket.DeleteObject(name)
}

func (h *OSSHandler) Size(name string) (int64, error) {
	meta, err := h.bucket.GetObjectDetailedMeta(name)
	if err != nil {
		return 0, err
	}
	return strconvInt64(meta.Get("Content-Length"))
}

func (h *OSSHandler) PresignGet(name string, expires time.Duration) (string, error) {
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	return h.bucket.SignURL(name, alioss.HTTPGet, int64(expires.Seconds()))
}

// Copy 通过 OSS CopyObject 在同 bucket 内复制对象
func (h *OSSHandler) Copy(src, dst string) error {
	_, err := h.bucket.CopyObject(src, dst)
	return err
}

func strconvInt64(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n, nil
}
