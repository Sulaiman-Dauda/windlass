package update

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	in_toto "github.com/in-toto/attestation/go/v1"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The attestation GitHub holds for the v0.60 linux/amd64 release binary, and
// the SHA-256 of that binary. Verified offline against a snapshot of
// Sigstore's keys: the certificate is judged at the time the transparency
// log recorded the signature, so the fixture does not expire.
const (
	v060Digest       = "8ae64a5bd0d4a6137682993769f5564fbe1c7453e62d0ff2da0bcf864fe0edc8"
	v060Tag          = "v0.60"
	v060Attestations = "testdata/v0.60-linux-amd64.attestations.json"
)

func snapshotRoot(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("testdata/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	prev := trustedRoot
	trustedRoot = func(context.Context) (root.TrustedMaterial, error) {
		return root.NewTrustedRootFromJSON(b)
	}
	t.Cleanup(func() { trustedRoot = prev })
}

func setRepo(t *testing.T, repo string) {
	t.Helper()
	prev := Repo
	Repo = repo
	t.Cleanup(func() { Repo = prev })
}

// stubGitHub answers every request from routes, keyed by a URL path suffix.
// A request with no route fails the test and gets a 404.
func stubGitHub(t *testing.T, routes map[string]string) {
	t.Helper()
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		for suffix, body := range routes {
			if strings.HasSuffix(r.URL.Path, suffix) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			}
		}
		if !strings.Contains(r.URL.Path, "/attestations/") {
			t.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
}

func realAttestations(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(v060Attestations)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serveAttestations answers any digest lookup with body, the way a rewritten
// release would have to: by replaying an attestation that does exist.
func serveAttestations(t *testing.T, body string) {
	t.Helper()
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/attestations/sha256:") {
			t.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
}

func TestProvenanceAcceptsTheReleaseBinary(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	if err := verifyProvenance(context.Background(), v060Digest, v060Tag); err != nil {
		t.Fatalf("the genuine v0.60 binary was refused: %v", err)
	}
}

func TestProvenanceIgnoresRepositoryCase(t *testing.T) {
	snapshotRoot(t)
	setRepo(t, "sulaiman-dauda/WINDLASS")
	serveAttestations(t, realAttestations(t))
	if err := verifyProvenance(context.Background(), v060Digest, v060Tag); err != nil {
		t.Fatalf("refused over the case of the repository name: %v", err)
	}
}

func TestProvenanceRefusesOtherBytes(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	other := sha256.Sum256([]byte("a rewritten binary"))
	if err := verifyProvenance(context.Background(), hex.EncodeToString(other[:]), v060Tag); err == nil {
		t.Fatal("a genuine attestation was accepted for a different binary")
	}
}

func TestProvenanceRefusesAnotherTag(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	for _, tag := range []string{"v0.61", "v0.6", "v0.600", "V0.60", ""} {
		if err := verifyProvenance(context.Background(), v060Digest, tag); err == nil {
			t.Errorf("the v0.60 build was accepted as release %q", tag)
		}
	}
}

func TestProvenanceRefusesAnotherRepository(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	for _, repo := range []string{"someone-else/windlass", "Sulaiman-Dauda/windlass-fork", "Sulaiman-Dauda/windlas."} {
		setRepo(t, repo)
		if err := verifyProvenance(context.Background(), v060Digest, v060Tag); err == nil {
			t.Errorf("a Sulaiman-Dauda/windlass build was accepted for %s", repo)
		}
	}
}

// rewriteBundle applies edit to the first bundle in the fixture.
func rewriteBundle(t *testing.T, edit func(bundle map[string]any)) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(realAttestations(t)), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc["attestations"].([]any)[0].(map[string]any)["bundle"].(map[string]any))
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestProvenanceRefusesARewrittenStatement(t *testing.T) {
	snapshotRoot(t)
	other := sha256.Sum256([]byte("a rewritten binary"))
	otherHex := hex.EncodeToString(other[:])
	// The statement now names the attacker's binary, but the signature was
	// made over the original statement.
	body := rewriteBundle(t, func(b map[string]any) {
		env := b["dsseEnvelope"].(map[string]any)
		payload, err := base64.StdEncoding.DecodeString(env["payload"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), v060Digest) {
			t.Fatal("fixture statement does not name the v0.60 digest")
		}
		env["payload"] = base64.StdEncoding.EncodeToString(
			[]byte(strings.ReplaceAll(string(payload), v060Digest, otherHex)))
	})
	serveAttestations(t, body)
	if err := verifyProvenance(context.Background(), otherHex, v060Tag); err == nil {
		t.Fatal("a statement edited after signing was accepted")
	}
}

func TestProvenanceRefusesWithoutALogEntry(t *testing.T) {
	snapshotRoot(t)
	body := rewriteBundle(t, func(b map[string]any) {
		vm := b["verificationMaterial"].(map[string]any)
		if _, ok := vm["tlogEntries"]; !ok {
			t.Fatal("fixture has no tlogEntries")
		}
		delete(vm, "tlogEntries")
	})
	serveAttestations(t, body)
	if err := verifyProvenance(context.Background(), v060Digest, v060Tag); err == nil {
		t.Fatal("an attestation with no transparency log entry was accepted")
	}
}

func TestProvenanceRefusesWhenGitHubHoldsNone(t *testing.T) {
	snapshotRoot(t)
	for name, body := range map[string]string{
		"not found": "",
		"empty":     `{"attestations":[]}`,
		"no bundle": `{"attestations":[{"bundle":null}]}`,
		"junk":      `{"attestations":[{"bundle":{"mediaType":"x"}}]}`,
		"not json":  `<html>`,
	} {
		if body == "" {
			stubGitHub(t, nil)
		} else {
			serveAttestations(t, body)
		}
		if err := verifyProvenance(context.Background(), v060Digest, v060Tag); err == nil {
			t.Errorf("%s: accepted a binary with no attestation", name)
		}
	}
}

func TestProvenanceRefusesWithoutSigstoreKeys(t *testing.T) {
	prev := trustedRoot
	trustedRoot = func(context.Context) (root.TrustedMaterial, error) {
		return nil, errors.New("tuf unreachable")
	}
	t.Cleanup(func() { trustedRoot = prev })
	serveAttestations(t, realAttestations(t))
	err := verifyProvenance(context.Background(), v060Digest, v060Tag)
	if err == nil || !strings.Contains(err.Error(), "tuf unreachable") {
		t.Fatalf("verifyProvenance = %v, want the key fetch failure", err)
	}
}

func TestCheckBuild(t *testing.T) {
	good := func() *verify.VerificationResult {
		return &verify.VerificationResult{
			Statement: &in_toto.Statement{PredicateType: provenancePredicate},
			Signature: &verify.SignatureVerificationResult{Certificate: &certificate.Summary{
				Extensions: certificate.Extensions{
					SourceRepositoryURI: "https://github.com/Sulaiman-Dauda/windlass",
					SourceRepositoryRef: "refs/tags/v0.60",
					RunnerEnvironment:   "github-hosted",
				},
			}},
		}
	}
	if err := checkBuild(good(), "v0.60"); err != nil {
		t.Fatalf("a good build was refused: %v", err)
	}
	for name, edit := range map[string]func(*verify.VerificationResult){
		"another statement type": func(r *verify.VerificationResult) {
			r.Statement.PredicateType = "https://spdx.dev/Document/v2.3"
		},
		"another source repository": func(r *verify.VerificationResult) {
			r.Signature.Certificate.SourceRepositoryURI = "https://github.com/someone-else/windlass"
		},
		"a branch": func(r *verify.VerificationResult) {
			r.Signature.Certificate.SourceRepositoryRef = "refs/heads/main"
		},
		"a self-hosted runner": func(r *verify.VerificationResult) {
			r.Signature.Certificate.RunnerEnvironment = "self-hosted"
		},
		"no statement":   func(r *verify.VerificationResult) { r.Statement = nil },
		"no certificate": func(r *verify.VerificationResult) { r.Signature.Certificate = nil },
	} {
		r := good()
		edit(r)
		if err := checkBuild(r, "v0.60"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := checkBuild(nil, "v0.60"); err == nil {
		t.Error("nil result accepted")
	}
}

// A release rewritten by someone who can edit it: the binary and
// checksums.txt agree with each other, so the checksum passes.
func rewrittenRelease(t *testing.T) (Release, map[string]string) {
	t.Helper()
	binary := "#!/bin/sh\necho not windlass\n"
	sum := sha256.Sum256([]byte(binary))
	return Release{
		Version:     v060Tag,
		assetURL:    "https://example.invalid/bin",
		checksumURL: "https://example.invalid/sums",
	}, map[string]string{
		"/bin":  binary,
		"/sums": hex.EncodeToString(sum[:]) + "  windlass-linux-" + runtime.GOARCH + "\n",
	}
}

func TestFetchVerifiedRefusesARewrittenRelease(t *testing.T) {
	snapshotRoot(t)
	rel, routes := rewrittenRelease(t)
	for name, attestations := range map[string]string{
		"no attestation":       "",
		"replayed attestation": realAttestations(t),
	} {
		if attestations != "" {
			routes["/attestations/sha256:"+sha256Hex(routes["/bin"])] = attestations
		}
		stubGitHub(t, routes)
		dir := t.TempDir()
		svc := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") })
		path, err := svc.fetchVerified(context.Background(), rel, dir)
		if err == nil || !strings.Contains(err.Error(), "build provenance") {
			t.Errorf("%s: fetchVerified = %q, %v; want a build provenance refusal", name, path, err)
		}
		left, _ := filepath.Glob(filepath.Join(dir, "*"))
		if len(left) != 0 {
			t.Errorf("%s: refused binary left on disk: %v", name, left)
		}
	}
}

func TestFetchVerifiedStillChecksTheChecksum(t *testing.T) {
	snapshotRoot(t)
	rel, routes := rewrittenRelease(t)
	routes["/bin"] = "truncated"
	stubGitHub(t, routes)
	dir := t.TempDir()
	svc := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") })
	_, err := svc.fetchVerified(context.Background(), rel, dir)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("fetchVerified = %v, want a checksum mismatch", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 0 {
		t.Errorf("mismatched binary left on disk: %v", left)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
