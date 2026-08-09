package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3Config struct {
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`
	AccessKey      string `json:"accessKey"`
	SecretKey      string `json:"secretKey"`
	ForcePathStyle bool   `json:"forcePathStyle"`
	SSE            string `json:"sse"`
}

type S3Handler struct {
	cfg    s3Config
	client *s3.Client
}

func newS3Handler(cfgJSON string) (Handler, error) {
	var cfg s3Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return nil, errors.New("s3 policy config invalid json: " + err.Error())
	}
	if cfg.Bucket == "" {
		return nil, errors.New("s3 policy config missing key: bucket")
	}
	if cfg.Region == "" {
		return nil, errors.New("s3 policy config missing key: region")
	}
	if cfg.AccessKey == "" {
		return nil, errors.New("s3 policy config missing key: accessKey")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("s3 policy config missing key: secretKey")
	}

	ctx := context.Background()
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	}
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	s3opts := func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}
	client := s3.NewFromConfig(awscfg, s3opts)
	return &S3Handler{cfg: cfg, client: client}, nil
}

func s3UploadConfig(size int64) (partSize int64, concurrency int) {
	partSize = 10 * 1024 * 1024
	concurrency = 5
	if size < 100*1024*1024 {
		return 0, 0
	}
	if size > 5*1024*1024*1024 {
		partSize = 20 * 1024 * 1024
		concurrency = 16
	}
	return partSize, concurrency
}

func (h *S3Handler) Put(src io.Reader, name string, size int64) error {
	ctx := context.Background()
	if size < 100*1024*1024 {
		input := &s3.PutObjectInput{
			Bucket: aws.String(h.cfg.Bucket),
			Key:    aws.String(name),
			Body:   src,
		}
		if h.cfg.SSE != "" {
			input.ServerSideEncryption = s3types.ServerSideEncryption(h.cfg.SSE)
		}
		_, err := h.client.PutObject(ctx, input)
		return err
	}
	partSize, concurrency := s3UploadConfig(size)
	uploader := manager.NewUploader(h.client, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = concurrency
	})
	input := &s3.PutObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
		Body:   src,
	}
	if h.cfg.SSE != "" {
		input.ServerSideEncryption = s3types.ServerSideEncryption(h.cfg.SSE)
	}
	_, err := uploader.Upload(ctx, input)
	return err
}

func (h *S3Handler) Get(name string) (io.ReadCloser, error) {
	ctx := context.Background()
	out, err := h.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (h *S3Handler) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	ctx := context.Background()
	var rng string
	if length <= 0 {
		rng = fmt.Sprintf("bytes=%d-", offset)
	} else {
		rng = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}
	out, err := h.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
		Range:  aws.String(rng),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (h *S3Handler) Delete(name string) error {
	ctx := context.Background()
	_, err := h.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
	})
	return err
}

func (h *S3Handler) Size(name string) (int64, error) {
	ctx := context.Background()
	out, err := h.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
	})
	if err != nil {
		return 0, err
	}
	if out.ContentLength == nil {
		return 0, errors.New("s3 head object content length nil")
	}
	return *out.ContentLength, nil
}

func (h *S3Handler) PresignGet(name string, expires time.Duration) (string, error) {
	ctx := context.Background()
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	pc := s3.NewPresignClient(h.client)
	req, err := pc.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.cfg.Bucket),
		Key:    aws.String(name),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// Copy 通过 S3 CopyObject 在同 bucket 内复制对象
func (h *S3Handler) Copy(src, dst string) error {
	ctx := context.Background()
	_, err := h.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(h.cfg.Bucket),
		CopySource: aws.String(h.cfg.Bucket + "/" + src),
		Key:        aws.String(dst),
	})
	return err
}
