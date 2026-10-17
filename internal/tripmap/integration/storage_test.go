package integration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent"
)

func TestPostgresSchema(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN for live PostgreSQL checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "tripmap_test_" + uuid.New().String()[:8]
	if _, err = db.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	defer scoped.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	if err = client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := client.User.Create().SetDisplayName("integration test").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	visit := func() *ent.CityVisitCreate {
		return client.CityVisit.Create().SetUserID(user.ID).SetRegionCode("330100").SetRegionName("杭州市").SetProvinceCode("330000").SetColor("#4b937d").SetTravelDate("2026-10-12").SetNote("西湖")
	}
	if _, err = visit().Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = visit().Save(ctx); !ent.IsConstraintError(err) {
		t.Fatalf("duplicate city accepted: %v", err)
	}
	if _, err = client.ProvinceMark.Create().SetUserID(user.ID).SetProvinceCode("330000").SetColor("#719fc6").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = visit().SetRegionCode("330200").SetColor("invalid").Save(ctx); err == nil {
		t.Fatal("invalid color accepted")
	}
	if _, err = visit().SetRegionCode("330200").SetTravelDate("2026-02-30").Save(ctx); err == nil {
		t.Fatal("invalid calendar date accepted")
	}
	if _, err = visit().SetRegionCode("330200").SetNote(strings.Repeat("🌊", 501)).Save(ctx); err == nil {
		t.Fatal("note exceeding frontend UTF-16 limit accepted")
	}
	if _, err = client.CityVisit.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := client.ProvinceMark.Query().Count(ctx); err != nil || count != 1 {
		t.Fatalf("city deletion affected province: %d %v", count, err)
	}
	if err = client.User.DeleteOneID(user.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := client.ProvinceMark.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("owner cascade failed: %d %v", count, err)
	}
}
func TestRedisRoundTrip(t *testing.T) {
	address := os.Getenv("TRIPMAP_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TRIPMAP_TEST_REDIS_ADDR for live Redis checks")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("TRIPMAP_TEST_REDIS_PASSWORD")})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := "tripmap:test:" + uuid.NewString()
	defer client.Del(context.Background(), key)
	if err := client.Set(ctx, key, "roundtrip", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if value, err := client.Get(ctx, key).Result(); err != nil || value != "roundtrip" {
		t.Fatalf("redis roundtrip: %q %v", value, err)
	}
}
func TestS3RoundTrip(t *testing.T) {
	endpoint := os.Getenv("TRIPMAP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TRIPMAP_TEST_S3_ENDPOINT for live S3 checks")
	}
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("TRIPMAP_TEST_S3_ACCESS_KEY"), os.Getenv("TRIPMAP_TEST_S3_SECRET_KEY"), "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	store := blobstore.NewS3(client, os.Getenv("TRIPMAP_TEST_S3_BUCKET"), "integration-tests")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := store.Check(ctx); err != nil {
		t.Skip("S3 endpoint configured but unreachable:", err)
	}
	key := uuid.NewString() + ".txt"
	defer store.Delete(context.Background(), key)
	data := []byte("tripmap s3 integration")
	if err := store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(body, data) {
		t.Fatalf("S3 roundtrip: %q %v", body, err)
	}
	if err = store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if reader, err = store.Get(ctx, key); !errors.Is(err, blobstore.ErrNotFound) {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("deleted object still readable: %v", err)
	}
}
