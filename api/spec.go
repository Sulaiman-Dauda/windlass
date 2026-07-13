// Package api embeds the hand-maintained OpenAPI specification so the
// binary serves its own documentation.
package api

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
