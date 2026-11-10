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
// and would then block every later update. A variable so tests verify
// offline.
var trustedRoot = func(ctx context.Context) (root.TrustedMaterial, error) {
	type result struct {
		tr  *root.TrustedRoot
		err error
	}
	ctx, cancel := context.WithTimeout(ctx, trustedRootTimeout)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		tr, err := root.FetchTrustedRootWithOptions(
			tuf.DefaultOptions().WithDisableLocalCache().WithContext(ctx))
		done <- result{tr, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return r.tr, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// verifyProvenance checks that the binary with this SHA-256 was built by the
// release workflow of Repo at the tag being installed. It returns nil only
// when one attestation passes every check.
func verifyProvenance(ctx context.Context, digestHex, tag string) error {
	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 {
		return errors.New("build provenance: not a SHA-256 digest")
	}
	if tag == "" {
		return errors.New("build provenance: release has no tag")
	}

	bundles, err := fetchAttestations(ctx, digestHex)
	if err != nil {
		return err
	}
	material, err := trustedRoot(ctx)
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
		if err := checkBuild(res, tag); err != nil {
			last = err
			continue
		}
		return nil
	}
	return fmt.Errorf("build provenance: no attestation for this binary verified: %w", last)
}

// checkBuild holds a verified attestation to what the signature alone does
// not say: the statement is build provenance, the source is this repository
// at this tag, and the build ran on GitHub's own runners.
func checkBuild(res *verify.VerificationResult, tag string) error {
	if res == nil || res.Statement == nil || res.Signature == nil || res.Signature.Certificate == nil {
		return errors.New("attestation carries no statement or certificate")
	}
	if got := res.Statement.GetPredicateType(); got != provenancePredicate {
		return fmt.Errorf("attestation is %q, not build provenance", got)
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
		"https://api.github.com/repos/"+Repo+"/attestations/sha256:"+digestHex, nil)
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
