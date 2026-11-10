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

	"github.com/windlass-dev/windlass/internal/version"
)

// The attestation GitHub holds for the v0.60 linux/amd64 release binary, and
// the SHA-256 of that binary. Verified offline against a snapshot of
// Sigstore's keys: the certificate is judged at the time the transparency
// log recorded the signature, so the fixture does not expire.
const (
	v060Digest       = "8ae64a5bd0d4a6137682993769f5564fbe1c7453e62d0ff2da0bcf864fe0edc8"
	v060Asset        = "windlass-linux-amd64"
	v060ArmDigest    = "93684078d6791b9bdc3027b0389df1adcea20672cce18aa08c9dc7fca2616276"
	v060ArmAsset     = "windlass-linux-arm64"
	v060Tag          = "v0.60"
	v060Attestations = "testdata/v0.60-linux-amd64.attestations.json"
)

// verifyV060 checks a digest as the linux/amd64 asset of a release.
func verifyV060(digest, tag string) error {
	return verifyProvenance(context.Background(), artifact{name: v060Asset, sha256: digest, tag: tag}, "")
}

func snapshotRoot(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("testdata/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	prev := trustedRoot
	trustedRoot = func(context.Context, string) (root.TrustedMaterial, error) {
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
	if err := verifyV060(v060Digest, v060Tag); err != nil {
		t.Fatalf("the genuine v0.60 binary was refused: %v", err)
	}
}

func TestProvenanceIgnoresRepositoryCase(t *testing.T) {
	snapshotRoot(t)
	setRepo(t, "sulaiman-dauda/WINDLASS")
	serveAttestations(t, realAttestations(t))
	if err := verifyV060(v060Digest, v060Tag); err != nil {
		t.Fatalf("refused over the case of the repository name: %v", err)
	}
}

func TestProvenanceRefusesOtherBytes(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	other := sha256.Sum256([]byte("a rewritten binary"))
	if err := verifyV060(hex.EncodeToString(other[:]), v060Tag); err == nil {
		t.Fatal("a genuine attestation was accepted for a different binary")
	}
}

func TestProvenanceRefusesAnotherTag(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	for _, tag := range []string{"v0.61", "v0.6", "v0.600", "V0.60", ""} {
		if err := verifyV060(v060Digest, tag); err == nil {
			t.Errorf("the v0.60 build was accepted as release %q", tag)
		}
	}
}

func TestProvenanceRefusesAnotherRepository(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	for _, repo := range []string{"someone-else/windlass", "Sulaiman-Dauda/windlass-fork", "Sulaiman-Dauda/windlas."} {
		setRepo(t, repo)
		if err := verifyV060(v060Digest, v060Tag); err == nil {
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
	if err := verifyV060(otherHex, v060Tag); err == nil {
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
	if err := verifyV060(v060Digest, v060Tag); err == nil {
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
		if err := verifyV060(v060Digest, v060Tag); err == nil {
			t.Errorf("%s: accepted a binary with no attestation", name)
		}
	}
}

func TestProvenanceRefusesWithoutSigstoreKeys(t *testing.T) {
	prev := trustedRoot
	trustedRoot = func(context.Context, string) (root.TrustedMaterial, error) {
		return nil, errors.New("tuf unreachable")
	}
	t.Cleanup(func() { trustedRoot = prev })
	serveAttestations(t, realAttestations(t))
	err := verifyV060(v060Digest, v060Tag)
	if err == nil || !strings.Contains(err.Error(), "tuf unreachable") {
		t.Fatalf("verifyProvenance = %v, want the key fetch failure", err)
	}
}

func TestCheckBuild(t *testing.T) {
	a := artifact{name: v060Asset, sha256: v060Digest, tag: "v0.60"}
	good := func() *verify.VerificationResult {
		return &verify.VerificationResult{
			Statement: &in_toto.Statement{
				PredicateType: provenancePredicate,
				Subject: []*in_toto.ResourceDescriptor{
					{Name: v060Asset, Digest: map[string]string{"sha256": v060Digest}},
					{Name: v060ArmAsset, Digest: map[string]string{"sha256": v060ArmDigest}},
				},
			},
			Signature: &verify.SignatureVerificationResult{Certificate: &certificate.Summary{
				Extensions: certificate.Extensions{
					SourceRepositoryURI: "https://github.com/Sulaiman-Dauda/windlass",
					SourceRepositoryRef: "refs/tags/v0.60",
					RunnerEnvironment:   "github-hosted",
				},
			}},
		}
	}
	if err := checkBuild(good(), a); err != nil {
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
		"the digest under another name": func(r *verify.VerificationResult) {
			r.Statement.Subject[0].Name = "install.sh"
		},
		"no subjects":    func(r *verify.VerificationResult) { r.Statement.Subject = nil },
		"no statement":   func(r *verify.VerificationResult) { r.Statement = nil },
		"no certificate": func(r *verify.VerificationResult) { r.Signature.Certificate = nil },
	} {
		r := good()
		edit(r)
		if err := checkBuild(r, a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := checkBuild(nil, a); err == nil {
		t.Error("nil result accepted")
	}
}

// The one statement of a release names both binaries, and the signature
// check accepts either digest. Only checkBuild ties a digest to its name, so
// this also fails if verifyProvenance stops calling it.
func TestProvenanceRefusesTheOtherArchitecture(t *testing.T) {
	snapshotRoot(t)
	serveAttestations(t, realAttestations(t))
	ctx := context.Background()
	arm := artifact{name: v060ArmAsset, sha256: v060ArmDigest, tag: v060Tag}
	if err := verifyProvenance(ctx, arm, ""); err != nil {
		t.Fatalf("the genuine arm64 binary was refused: %v", err)
	}
	swapped := artifact{name: v060Asset, sha256: v060ArmDigest, tag: v060Tag}
	err := verifyProvenance(ctx, swapped, "")
	if err == nil || !strings.Contains(err.Error(), "does not name these bytes") {
		t.Fatalf("the arm64 binary as the amd64 asset: %v, want a refusal by name", err)
	}
}

// Without a certificate transparency log to check against, the proof inside
// the signing certificate cannot be verified, and that must refuse.
func TestProvenanceRefusesWithoutCertificateTransparency(t *testing.T) {
	b, err := os.ReadFile("testdata/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if logs, _ := doc["ctlogs"].([]any); len(logs) == 0 {
		t.Fatal("fixture trusted root has no ctlogs")
	}
	delete(doc, "ctlogs")
	stripped, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	prev := trustedRoot
	trustedRoot = func(context.Context, string) (root.TrustedMaterial, error) {
		return root.NewTrustedRootFromJSON(stripped)
	}
	t.Cleanup(func() { trustedRoot = prev })
	serveAttestations(t, realAttestations(t))
	if err := verifyV060(v060Digest, v060Tag); err == nil {
		t.Fatal("accepted with no certificate transparency log to verify against")
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
	for _, c := range []struct {
		name         string
		attestations string
		want         string
	}{
		{"no attestation", "", "GitHub holds no attestation"},
		{"replayed attestation", realAttestations(t), "does not match any digest"},
	} {
		rel, routes := rewrittenRelease(t)
		if c.attestations != "" {
			routes["/attestations/sha256:"+sha256Hex(routes["/bin"])] = c.attestations
		}
		stubGitHub(t, routes)
		dir := t.TempDir()
		svc := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") })
		path, err := svc.fetchVerified(context.Background(), rel, dir)
		if err == nil || !strings.Contains(err.Error(), "build provenance") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: fetchVerified = %q, %v; want a build provenance refusal with %q", c.name, path, err, c.want)
		}
		left, _ := filepath.Glob(filepath.Join(dir, "*"))
		if len(left) != 0 {
			t.Errorf("%s: refused binary left on disk: %v", c.name, left)
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

func TestOlderReleaseIsNotAnUpdate(t *testing.T) {
	for _, c := range []struct {
		release, current string
		older            bool
	}{
		{"v0.60", "v0.61", true},
		{"v0.9", "v0.10", true},
		{"v0.60", "0.61", true},
		{"v0.60", "v0.60.1", true},
		{"v0.61", "v1.0", true},
		{"v0.61", "v0.60", false},
		{"v0.60.1", "v0.60", false},
		{"v0.61", "v0.61", false},
		{"v0.61", "dev", false},
		{"nightly", "v0.61", false},
		{"v0.61-rc1", "v0.61", false},
	} {
		if got := olderRelease(c.release, c.current); got != c.older {
			t.Errorf("olderRelease(%q, %q) = %v, want %v", c.release, c.current, got, c.older)
		}
	}
}

// A second Apply would download over the file the first has verified.
func TestApplyRunsOneAtATime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("self-update is Linux only")
	}
	t.Setenv("WINDLASS_NO_SELF_UPDATE", "")
	// The release lookup fails, so an Apply that gets past the lock stops there.
	stubGitHub(t, map[string]string{"/releases/latest": "not JSON"})
	svc := New(slog.New(slog.DiscardHandler), t.TempDir(), func() { t.Error("restart called") })
	svc.applying.Lock()
	err := svc.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already being installed") {
		t.Fatalf("Apply during another Apply = %v, want a refusal", err)
	}
	svc.applying.Unlock()
	// A failed Apply gives the lock back.
	if err := svc.Apply(context.Background()); err == nil || strings.Contains(err.Error(), "already being installed") {
		t.Fatalf("Apply after the first finished = %v, want its own failure", err)
	}
	if !svc.applying.TryLock() {
		t.Fatal("a failed Apply kept the lock")
	}
}

// An older release marked latest carries genuine provenance, so Check is the
// only place that keeps it from being installed.
func TestCheckDoesNotOfferAnOlderRelease(t *testing.T) {
	prev := version.Version
	version.Version = "v0.61"
	t.Cleanup(func() { version.Version = prev })
	for tag, want := range map[string]bool{"v0.60": false, "v0.61": false, "v0.62": true} {
		stubGitHub(t, map[string]string{"/releases/latest": `{"tag_name":"` + tag + `","assets":[` +
			`{"name":"windlass-linux-` + runtime.GOARCH + `","browser_download_url":"https://example.invalid/bin"}]}`})
		svc := New(slog.New(slog.DiscardHandler), t.TempDir(), func() {})
		rel, err := svc.Check(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if rel.UpdateReady != want {
			t.Errorf("running v0.61, latest %s: UpdateReady = %v, want %v", tag, rel.UpdateReady, want)
		}
	}
}
