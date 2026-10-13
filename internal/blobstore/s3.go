package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var ErrNotFound = errors.New("object not found")

// Store keeps object access separate from upload or snapshot business logic.
type Store interface {
	Put(context.Context, string, io.ReadSeeker, int64, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
type API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}
type S3 struct {
	client API
	bucket string
	prefix string
	signer *s3.PresignClient
}

func NewS3(client API, bucket, prefix string) *S3 {
	s := &S3{client: client, bucket: bucket, prefix: strings.Trim(prefix, "/")}
	if c, ok := client.(*s3.Client); ok {
		s.signer = s3.NewPresignClient(c)
	}
	return s
}

// UploadSigner is optional so other object stores and test doubles remain small.
type UploadSigner interface {
	SignUpload(context.Context, string, int64, string, time.Duration) (string, map[string]string, error)
}

func (s *S3) SignUpload(ctx context.Context, key string, size int64, mime string, ttl time.Duration) (string, map[string]string, error) {
	k, err := s.key(key)
	if err != nil {
		return "", nil, err
	}
	if s.signer == nil || size <= 0 || ttl <= 0 {
		return "", nil, fmt.Errorf("upload signing unavailable")
	}
	var out *v4.PresignedHTTPRequest
	out, err = s.signer.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k), ContentLength: aws.Int64(size), ContentType: aws.String(mime)}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", nil, err
	}
	headers := map[string]string{"Content-Type": mime}
	for name, values := range out.SignedHeader {
		// Browsers set Host and Content-Length from the request URL and File body.
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") {
			continue
		}
		headers[name] = strings.Join(values, ",")
	}
	return out.URL, headers, nil
}
func (s *S3) key(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return "", fmt.Errorf("invalid object key")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid object key")
		}
	}
	if s.prefix != "" {
		return s.prefix + "/" + key, nil
	}
	return key, nil
}
func (s *S3) Check(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	return err
}
func (s *S3) Put(ctx context.Context, key string, r io.ReadSeeker, size int64, contentType string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("object size must be non-negative")
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k), Body: r, ContentLength: aws.Int64(size), ContentType: aws.String(contentType)})
	return err
}
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
	return err
}
