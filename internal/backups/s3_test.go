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

func TestEscapeS3Key(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"project/archive.tar.gz", "project/archive.tar.gz"},
		{"project/archive name.tar.gz", "project/archive%20name.tar.gz"},
		{"a&b/c=d/e,f.tar.gz", "a%26b/c%3Dd/e%2Cf.tar.gz"},
		{"my+project/x.tar.gz", "my%2Bproject/x.tar.gz"},
		{"team+eu/proj/x.tar.gz", "team%2Beu/proj/x.tar.gz"},
		{"café/x.tar.gz", "caf%C3%A9/x.tar.gz"},
		{"a~b_c-d.e/x", "a~b_c-d.e/x"},
		{"", ""},
		{"/", "/"},
		{"//", "//"},
	}
	for _, tt := range tests {
		if got := escapeS3Key(tt.in); got != tt.want {
			t.Errorf("escapeS3Key(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestObjectURLEscapesSigV4SpecialChars(t *testing.T) {
	client := newS3(S3Config{
		Endpoint: "https://s3.example.com", Region: "test", Bucket: "backups",
		AccessKey: "access", SecretKey: "secret", KeyPrefix: "team+eu/",
	})
	got := client.objectURL("proj/a&b=c.tar.gz")
	want := "https://s3.example.com/backups/team%2Beu/proj/a%26b%3Dc.tar.gz"
	if got != want {
		t.Fatalf("objectURL = %q, want %q", got, want)
	}
}
