package backups

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteObjectSignsAndDeletesRequestedKey(t *testing.T) {
	var method, path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.EscapedPath()
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newS3(S3Config{
		Endpoint: server.URL, Region: "test", Bucket: "backups",
		AccessKey: "access", SecretKey: "secret", KeyPrefix: "windlass/",
	})
	if err := client.DeleteObject(context.Background(), "project/archive name.tar.gz"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if method != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", method)
	}
	if path != "/backups/windlass/project/archive%20name.tar.gz" {
		t.Fatalf("escaped path = %q", path)
	}
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=access/") {
		t.Fatalf("request was not signed: %q", authorization)
	}
}

func TestDeleteObjectPreservesRecordOnRemoteFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := newS3(S3Config{
		Endpoint: server.URL, Region: "test", Bucket: "backups",
		AccessKey: "access", SecretKey: "secret",
	})
	err := client.DeleteObject(context.Background(), "archive.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("error = %v, want remote status", err)
	}
}
