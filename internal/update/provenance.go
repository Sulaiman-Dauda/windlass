package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

// checksums.txt sits in the release beside the binary, so whoever can rewrite
// the release can rewrite both. Build provenance is stored outside the
// release: GitHub Actions signs a statement that these exact bytes came out
// of this repository's release workflow at this tag, and the signature is
// recorded in Sigstore's public log. Apply refuses a binary without one.

const (
	// actionsIssuer is the OIDC issuer of every GitHub Actions run.
	actionsIssuer = "https://token.actions.githubusercontent.com"
	// releaseWorkflow is the only workflow allowed to have built a release.
	releaseWorkflow = ".github/workflows/release.yaml"
	// provenancePredicate is the statement type actions/attest-build-provenance signs.
	provenancePredicate = "https://slsa.dev/provenance/v1"
	// maxAttestationBytes bounds the attestations response. One bundle is
	// about 12 KiB.
	maxAttestationBytes = 4 << 20
	// trustedRootTimeout bounds the fetch of Sigstore's keys.
	trustedRootTimeout = 60 * time.Second
)

// trustedRoot returns Sigstore's current signing keys. They are fetched when
// an update is applied, starting from the TUF root compiled into the binary,
// because a copy baked into a release goes stale when Sigstore rotates a key
// and would then block every later update. Nothing is written to disk. The
// TUF client still reads metadata from its cache directory when some is
// there, and its default is under $HOME or the system temp directory, so it
// is pointed at tufDir, which only Windlass can write. A variable so tests
// verify offline.
var trustedRoot = func(ctx context.Context, tufDir string) (root.TrustedMaterial, error) {
	ctx, cancel := context.WithTimeout(ctx, trustedRootTimeout)
	defer cancel()
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPClient(contextClient{ctx})
	tr, err := root.FetchTrustedRootWithOptions(tuf.DefaultOptions().
		WithCachePath(tufDir).WithDisableLocalCache().WithFetcher(f))
	if err != nil {
		return nil, err
	}
	return tr, nil
}

// contextClient ties the TUF client's requests to a context. The TUF client
// builds them without one, so a stalled download would otherwise never end.
type contextClient struct{ ctx context.Context }

func (c contextClient) Do(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req.WithContext(c.ctx))
}

// artifact is the downloaded binary as the checks need to know it.
type artifact struct {
	// name is the release asset, windlass-linux-<arch>.
	name string
	// sha256 is the hex digest of the file on disk.
	sha256 string
	// tag is the release being installed.
	tag string
}

// verifyProvenance checks that the binary was built by the release workflow
// of Repo at the tag being installed, as the asset for this architecture. It
// returns nil only when one attestation passes every check. tufDir is where
// the TUF client may read cached metadata from.
func verifyProvenance(ctx context.Context, a artifact, tufDir string) error {
	digestHex, tag := a.sha256, a.tag
	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 {
		return errors.New("build provenance: not a SHA-256 digest")
	}
	if tag == "" || a.name == "" {
		return errors.New("build provenance: release has no tag or asset name")
	}

	bundles, err := fetchAttestations(ctx, digestHex)
	if err != nil {
		return err
	}
	material, err := trustedRoot(ctx, tufDir)
	if err != nil {
		return fmt.Errorf("build provenance: could not fetch Sigstore's signing keys: %w", err)
	}
	// One certificate transparency proof, one transparency log entry and a
	// signing time taken from that log, which is what GitHub issues for a
	// public repository.
	verifier, err := verify.NewVerifier(material,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
	if err != nil {
		return fmt.Errorf("build provenance: %w", err)
	}

	// The certificate's subject is the workflow file and the ref it ran at.
	// The repository compares without case because GitHub does; the tag does
	// not, so an older genuine build cannot stand in for this release.
	workflow := `^https://github\.com/(?i:` + regexp.QuoteMeta(Repo) + `)/` +
		regexp.QuoteMeta(releaseWorkflow) + `@refs/tags/` + regexp.QuoteMeta(tag) + `$`
	identity, err := verify.NewShortCertificateIdentity(actionsIssuer, "", "", workflow)
	if err != nil {
		return fmt.Errorf("build provenance: %w", err)
	}
	policy := verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digest),
		verify.WithCertificateIdentity(identity))

	var last error
	for _, raw := range bundles {
		var b bundle.Bundle
		if err := b.UnmarshalJSON(raw); err != nil {
			last = err
			continue
		}
		res, err := verifier.Verify(&b, policy)
		if err != nil {
			last = err
			continue
		}
		if err := checkBuild(res, a); err != nil {
			last = err
			continue
		}
		return nil
	}
	return fmt.Errorf("build provenance: no attestation for this binary verified: %w", last)
}

// checkBuild holds a verified attestation to what the signature alone does
// not say: the statement is build provenance, it names these bytes as the
// asset for this architecture, the source is this repository at this tag,
// and the build ran on GitHub's own runners.
func checkBuild(res *verify.VerificationResult, a artifact) error {
	tag := a.tag
	if res == nil || res.Statement == nil || res.Signature == nil || res.Signature.Certificate == nil {
		return errors.New("attestation carries no statement or certificate")
	}
	if got := res.Statement.GetPredicateType(); got != provenancePredicate {
		return fmt.Errorf("attestation is %q, not build provenance", got)
	}
	// One statement covers every binary of a release. The signature check
	// accepts a match on any of them, so the other architecture's binary
	// under this asset's name would pass without this.
	named := false
	for _, s := range res.Statement.GetSubject() {
		if s.GetName() == a.name && strings.EqualFold(s.GetDigest()["sha256"], a.sha256) {
			named = true
		}
	}
	if !named {
		return fmt.Errorf("attestation does not name these bytes as %s", a.name)
	}
	cert := res.Signature.Certificate
	if want := "https://github.com/" + Repo; !strings.EqualFold(cert.SourceRepositoryURI, want) {
		return fmt.Errorf("built from %q, not %s", cert.SourceRepositoryURI, want)
	}
	if want := "refs/tags/" + tag; cert.SourceRepositoryRef != want {
		return fmt.Errorf("built from %q, not %s", cert.SourceRepositoryRef, want)
	}
	if cert.RunnerEnvironment != "github-hosted" {
		return fmt.Errorf("built on a %q runner, not a GitHub-hosted one", cert.RunnerEnvironment)
	}
	return nil
}

// fetchAttestations returns the Sigstore bundles GitHub holds for a digest in
// Repo. The API answers without a token for a public repository.
func fetchAttestations(ctx context.Context, digestHex string) ([]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/attestations/sha256:"+digestHex+"?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if Token != "" {
		req.Header.Set("Authorization", "Bearer "+Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("build provenance: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("build provenance: GitHub holds no attestation for this binary")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("build provenance: github attestations: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAttestationBytes+1))
	if err != nil {
		return nil, fmt.Errorf("build provenance: %w", err)
	}
	if len(body) > maxAttestationBytes {
		return nil, errors.New("build provenance: attestations response is too large")
	}
	var out struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("build provenance: %w", err)
	}
	bundles := make([]json.RawMessage, 0, len(out.Attestations))
	for _, a := range out.Attestations {
		if len(a.Bundle) > 0 {
			bundles = append(bundles, a.Bundle)
		}
	}
	if len(bundles) == 0 {
		return nil, errors.New("build provenance: GitHub holds no attestation for this binary")
	}
	return bundles, nil
}
