package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps an S3-compatible object store. A zero-value Client with
// disabled=true is returned when credentials are absent; callers must check
// IsConfigured before attempting operations.
type Client struct {
	mc       *minio.Client
	bucket   string
	disabled bool
}

// NewClient constructs an S3 Client from the supplied credentials.
// Returns a disabled no-op Client when endpoint or credentials are empty so
// the application starts without object storage configured.
func NewClient(endpoint, bucket, accessKey, secretKey string) (*Client, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" {
		return &Client{disabled: true}, nil
	}

	// minio.New expects just the host[:port], not a full URL.
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	secure := !strings.HasPrefix(endpoint, "http://")

	mc, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: init client: %w", err)
	}

	return &Client{mc: mc, bucket: bucket}, nil
}

// IsConfigured reports whether S3 credentials were provided at construction.
func (c *Client) IsConfigured() bool {
	return !c.disabled
}

// Upload streams r to the given key. size must be the exact byte count of r,
// or -1 to let the client buffer (slower, use only when size is unknown).
func (c *Client) Upload(ctx context.Context, key string, r interface {
	Read([]byte) (int, error)
}, size int64, contentType string) error {
	if c.disabled {
		return fmt.Errorf("s3: not configured")
	}
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("s3: upload %q: %w", key, err)
	}
	return nil
}

// Delete removes key from the bucket. A missing key is not an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	if c.disabled {
		return fmt.Errorf("s3: not configured")
	}
	err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	return nil
}

// Get downloads key and returns its bytes.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	if c.disabled {
		return nil, fmt.Errorf("s3: not configured")
	}
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3: get %q: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("s3: read %q: %w", key, err)
	}
	return data, nil
}

// PresignedURL returns a time-limited GET URL for key.
func (c *Client) PresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if c.disabled {
		return "", fmt.Errorf("s3: not configured")
	}
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expiry, url.Values{})
	if err != nil {
		return "", fmt.Errorf("s3: presign %q: %w", key, err)
	}
	return u.String(), nil
}

// PresignedDownloadURL returns a time-limited GET URL for key that forces the
// browser to download the object as filename rather than render it inline.
func (c *Client) PresignedDownloadURL(ctx context.Context, key, filename string, expiry time.Duration) (string, error) {
	if c.disabled {
		return "", fmt.Errorf("s3: not configured")
	}
	params := url.Values{}
	params.Set("response-content-disposition", fmt.Sprintf("attachment; filename=%q", filename))
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expiry, params)
	if err != nil {
		return "", fmt.Errorf("s3: presign %q: %w", key, err)
	}
	return u.String(), nil
}
