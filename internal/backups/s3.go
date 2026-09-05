package backups

// Minimal S3-compatible client: PUT and GET with AWS Signature V4. A full
// SDK is not justified for two verbs (principle 10). Works with AWS S3,
// Cloudflare R2, Backblaze B2, and MinIO.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type S3Config struct {
	// Endpoint like "https://s3.eu-central-1.amazonaws.com" or an R2/MinIO URL.
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	// KeyPrefix namespaces objects, e.g. "windlass/".
	KeyPrefix string `json:"key_prefix,omitempty"`
}

func (c S3Config) Configured() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

type s3Client struct {
	cfg  S3Config
	http *http.Client
}

func newS3(cfg S3Config) *s3Client {
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	return &s3Client{cfg: cfg, http: &http.Client{Timeout: 30 * time.Minute}}
}

func (c *s3Client) objectURL(key string) string {
	return strings.TrimSuffix(c.cfg.Endpoint, "/") + "/" + c.cfg.Bucket + "/" + escapeS3Key(c.cfg.KeyPrefix+key)
}

// escapeS3Key percent-encodes an S3 object key for use in a request URL while
// keeping "/" as a literal path separator. SigV4 canonicalisation requires every
// byte outside A-Za-z0-9-_.~ to be percent-encoded; url.PathEscape leaves
// sub-delims such as "&", "=", and "+" alone, which produces SignatureDoesNotMatch
// against S3-compatible backends when those characters appear in a key (or key_prefix).
func escapeS3Key(key string) string {
	var b strings.Builder
	b.Grow(len(key) + 8)
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c == '/':
			b.WriteByte(c)
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// PutFile uploads a local file.
func (c *s3Client) PutFile(ctx context.Context, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	// SigV4 needs the payload hash; stream the file twice (hash, then send)
	// rather than buffering archives in memory.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	payloadHash := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.objectURL(key), f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	c.sign(req, payloadHash)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 put: %s: %s", resp.Status, body)
	}
	return nil
}

// GetFile downloads an object to a local file.
func (c *s3Client) GetFile(ctx context.Context, key, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(key), nil)
	if err != nil {
		return err
	}
	c.sign(req, emptyPayloadHash)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 get: %s: %s", resp.Status, body)
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// DeleteObject removes a retained backup after a newer backup has completed.
func (c *s3Client) DeleteObject(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.objectURL(key), nil)
	if err != nil {
		return err
	}
	c.sign(req, emptyPayloadHash)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 delete: %s: %s", resp.Status, body)
	}
	return nil
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign implements AWS Signature Version 4 for a request with a known
// payload hash.
func (c *s3Client) sign(req *http.Request, payloadHash string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	// Canonical request. We sign host + x-amz-* only, which S3 accepts.
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, c.cfg.Region, "s3", "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashHex([]byte(canonicalRequest)),
	}, "\n")

	// Signing key derivation.
	kDate := hmacSHA256([]byte("AWS4"+c.cfg.SecretKey), dateStamp)
	kRegion := hmacSHA256(kDate, c.cfg.Region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.cfg.AccessKey, scope, signedHeaders, signature,
	))
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
