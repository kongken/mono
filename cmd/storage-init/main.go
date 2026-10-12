// storage-init provisions the configured bucket explicitly; the server never creates buckets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"butterfly.orx.me/core/mod"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"go.orx.me/apps/trip-map/internal/config"
	"gopkg.in/yaml.v3"
)

func run(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg struct {
		config.AppConfig `yaml:",inline"`
		Store            mod.StoreConfig `yaml:"store"`
	}
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	entry, ok := cfg.Store.S3[cfg.Storage.S3Store]
	if !ok || entry.Bucket == "" {
		return fmt.Errorf("configured S3 store/bucket is missing")
	}
	if entry.Region == "" {
		entry.Region = "us-east-1"
	}
	ak, sk := entry.AccessKeyID, entry.SecretAccessKey
	if ak == "" {
		ak = entry.AK
	}
	if sk == "" {
		sk = entry.SK
	}
	client := s3.NewFromConfig(aws.Config{Region: entry.Region, Credentials: credentials.NewStaticCredentialsProvider(ak, sk, entry.SessionToken)}, func(o *s3.Options) {
		o.UsePathStyle = entry.UsePathStyle
		if entry.Endpoint != "" {
			endpoint := entry.Endpoint
			if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
				scheme := "http://"
				if entry.UseSSL {
					scheme = "https://"
				}
				endpoint = scheme + endpoint
			}
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(entry.Bucket)})
	if err == nil {
		fmt.Println("S3 bucket already available")
		return nil
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || (apiErr.ErrorCode() != "NotFound" && apiErr.ErrorCode() != "NoSuchBucket") {
		return fmt.Errorf("check S3 bucket: %w", err)
	}
	input := &s3.CreateBucketInput{Bucket: aws.String(entry.Bucket)}
	if entry.Region != "us-east-1" {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(entry.Region)}
	}
	if _, err = client.CreateBucket(ctx, input); err != nil {
		return fmt.Errorf("create S3 bucket: %w", err)
	}
	fmt.Println("S3 bucket created (no public policy configured)")
	return nil
}
func main() {
	path := flag.String("config", "config.yaml", "YAML configuration file")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
