package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Store wraps S3 put/list/get/delete for archived log chunks.
type Store struct {
	cfg    Config
	client *s3.Client
}

func NewStore(ctx context.Context, cfg Config) (*Store, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("s3 archive not configured (set LOG_VIEWER_S3_ENABLED=true and LOG_VIEWER_S3_BUCKET)")
	}
	loadOpts := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	var clientOpts []func(*s3.Options)
	if cfg.Endpoint != "" {
		ep := cfg.Endpoint
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = cfg.ForcePathStyle
		})
	} else if cfg.ForcePathStyle {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}
	return &Store{cfg: cfg, client: s3.NewFromConfig(awsCfg, clientOpts...)}, nil
}

func (s *Store) Config() Config { return s.cfg }

// ObjectKey builds: {prefix}/v1/{ns}/{kind}/{workload}/{pod}/{YYYY-MM-DD}/{HHMMSS}.jsonl.gz
func (s *Store) ObjectKey(ns, kind, workload, pod string, at time.Time) string {
	day := at.UTC().Format("2006-01-02")
	stamp := at.UTC().Format("150405")
	parts := []string{
		s.cfg.Prefix, "v1",
		safePath(ns), safePath(kind), safePath(workload), safePath(pod),
		day, stamp + ".jsonl.gz",
	}
	return strings.Join(parts, "/")
}

func safePath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "_"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_")
	return replacer.Replace(v)
}

func (s *Store) PutGzip(ctx context.Context, key string, body []byte) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(buf.Bytes()),
		ContentType: aws.String("application/gzip"),
	})
	return err
}

type ObjectInfo struct {
	Key          string
	LastModified time.Time
	Size         int64
}

func (s *Store) ListPrefix(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	var out []ObjectInfo
	var token *string
	for {
		res, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.cfg.Bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range res.Contents {
			info := ObjectInfo{Key: aws.ToString(obj.Key), Size: aws.ToInt64(obj.Size)}
			if obj.LastModified != nil {
				info.LastModified = *obj.LastModified
			}
			out = append(out, info)
		}
		if !aws.ToBool(res.IsTruncated) {
			break
		}
		token = res.NextContinuationToken
	}
	return out, nil
}

func (s *Store) GetGunzip(ctx context.Context, key string) ([]byte, error) {
	res, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		// Not gzip — return as-is (compat).
		return raw, nil
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func (s *Store) DeleteKeys(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	const batch = 1000
	for i := 0; i < len(keys); i += batch {
		end := i + batch
		if end > len(keys) {
			end = len(keys)
		}
		objs := make([]types.ObjectIdentifier, 0, end-i)
		for _, k := range keys[i:end] {
			objs = append(objs, types.ObjectIdentifier{Key: aws.String(k)})
		}
		_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.cfg.Bucket),
			Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// CleanupOlderThan removes archived objects older than retention (by day folder / LastModified).
func (s *Store) CleanupOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	prefix := s.cfg.Prefix + "/v1/"
	objs, err := s.ListPrefix(ctx, prefix)
	if err != nil {
		return 0, err
	}
	var doomed []string
	for _, o := range objs {
		day, ok := dayFromKey(o.Key)
		if ok {
			if day.Before(cutoff.UTC().Truncate(24 * time.Hour)) {
				doomed = append(doomed, o.Key)
			}
			continue
		}
		if !o.LastModified.IsZero() && o.LastModified.Before(cutoff) {
			doomed = append(doomed, o.Key)
		}
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	if err := s.DeleteKeys(ctx, doomed); err != nil {
		return 0, err
	}
	log.Printf("s3 archive cleanup deleted=%d cutoff=%s", len(doomed), cutoff.UTC().Format(time.RFC3339))
	return len(doomed), nil
}

func dayFromKey(key string) (time.Time, bool) {
	// …/{YYYY-MM-DD}/{HHMMSS}.jsonl.gz
	parts := strings.Split(key, "/")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	day := parts[len(parts)-2]
	t, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
