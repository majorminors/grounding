package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Tier represents an access level for the grounding server.
type Tier int

const (
	TierFull   Tier = iota // All backends, full attribution.
	TierPublic             // Free backends only; full-tier backends and tools silent.
)

// String names the tier for logs.
func (t Tier) String() string {
	if t == TierPublic {
		return "public"
	}
	return "full"
}

type ctxKey struct{}

// WithTier returns a context carrying the given tier.
func WithTier(ctx context.Context, t Tier) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// GetTier returns the tier from ctx, defaulting to TierFull.
// This means stdio/CLI paths (context.Background()) get full access.
func GetTier(ctx context.Context) Tier {
	if t, ok := ctx.Value(ctxKey{}).(Tier); ok {
		return t
	}
	return TierFull
}

// ResolveTier determines the access tier from the configured auth
// token and the incoming HTTP request. Checks, in order:
//  1. Authorization: Bearer <token> header
//  2. ?token=<token> query parameter (for clients like Claude web
//     connectors that can't set custom headers)
func ResolveTier(authToken string, r *http.Request) Tier {
	if authToken == "" {
		return TierFull
	}
	// Check Authorization header.
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(bearer) > 0 && subtle.ConstantTimeCompare([]byte(bearer), []byte(authToken)) == 1 {
		return TierFull
	}
	// Check query parameter fallback.
	if qToken := r.URL.Query().Get("token"); qToken != "" {
		if subtle.ConstantTimeCompare([]byte(qToken), []byte(authToken)) == 1 {
			return TierFull
		}
	}
	return TierPublic
}
