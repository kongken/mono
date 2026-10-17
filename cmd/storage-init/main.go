// storage-init provisions the configured bucket explicitly; the server never creates buckets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"butterfly.orx.me/core/mod"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kongken/mono/internal/tripmap/config"
	"gopkg.in/yaml.v3"
)

func run(path string, origins string, stagingLifecycle bool) error {
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
		return configureMediaBucket(ctx, client, entry.Bucket, cfg.Storage.KeyPrefix, origins, stagingLifecycle)
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
	return configureMediaBucket(ctx, client, entry.Bucket, cfg.Storage.KeyPrefix, origins, stagingLifecycle)
}

// These opt-in operations preserve other rules. They are never run at service
// startup and do not grant public object access.
func configureMediaBucket(ctx context.Context, client *s3.Client, bucket, prefix, origins string, lifecycle bool) error {
	if origins != "" {
		allowed := strings.Split(origins, ",")
		for i, origin := range allowed {
			origin = strings.TrimSpace(origin)
			u, err := url.Parse(origin)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.Contains(origin, "*") {
				return fmt.Errorf("media CORS requires exact http(s) origins")
			}
			allowed[i] = origin
		}
		var rules []types.CORSRule
		out, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(bucket)})
		if err != nil {
			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchCORSConfiguration" {
				return fmt.Errorf("read bucket CORS: %w", err)
			}
		} else {
			for _, r := range out.CORSRules {
				if aws.ToString(r.ID) != "tripmap-media-upload" {
					rules = append(rules, r)
				}
			}
		}
		rules = append(rules, types.CORSRule{ID: aws.String("tripmap-media-upload"), AllowedOrigins: allowed, AllowedMethods: []string{"PUT"}, AllowedHeaders: []string{"content-type", "content-length", "x-amz-*"}, MaxAgeSeconds: aws.Int32(600)})
		if _, err = client.PutBucketCors(ctx, &s3.PutBucketCorsInput{Bucket: aws.String(bucket), CORSConfiguration: &types.CORSConfiguration{CORSRules: rules}}); err != nil {
			return fmt.Errorf("set media CORS: %w", err)
		}
		fmt.Println("S3 media upload CORS configured")
	}
	if lifecycle {
		var rules []types.LifecycleRule
		out, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
		if err != nil {
			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchLifecycleConfiguration" {
				return fmt.Errorf("read bucket lifecycle: %w", err)
			}
		} else {
			for _, r := range out.Rules {
				if aws.ToString(r.ID) != "tripmap-media-staging" {
					rules = append(rules, r)
				}
			}
		}
		staging := strings.Trim(prefix, "/")
		if staging != "" {
			staging += "/"
		}
		staging += "media/staging/"
		rules = append(rules, types.LifecycleRule{ID: aws.String("tripmap-media-staging"), Status: types.ExpirationStatusEnabled, Filter: &types.LifecycleRuleFilter{Prefix: aws.String(staging)}, Expiration: &types.LifecycleExpiration{Days: aws.Int32(2)}})
		if _, err = client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{Bucket: aws.String(bucket), LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: rules}}); err != nil {
			return fmt.Errorf("set staging lifecycle: %w", err)
		}
		fmt.Println("S3 staging lifecycle configured")
	}
	return nil
}
func main() {
	path := flag.String("config", "config.tripmap.yaml", "YAML configuration file")
	origins := flag.String("media-cors-origins", "", "comma-separated frontend origins allowed to PUT media")
	lifecycle := flag.Bool("media-staging-lifecycle", false, "expire temporary media uploads after two days, preserving other lifecycle rules")
	flag.Parse()
	if err := run(*path, *origins, *lifecycle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
