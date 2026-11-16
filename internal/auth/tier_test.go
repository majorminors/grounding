package auth

import (
	"context"
	"net/http"
	"testing"
)

func TestGetTierDefaultsFull(t *testing.T) {
	ctx := context.Background()
	if got := GetTier(ctx); got != TierFull {
		t.Errorf("GetTier(Background) = %d, want TierFull", got)
	}
}

func TestWithTierRoundtrip(t *testing.T) {
	ctx := WithTier(context.Background(), TierPublic)
	if got := GetTier(ctx); got != TierPublic {
		t.Errorf("GetTier = %d, want TierPublic", got)
	}
}

func TestResolveTier(t *testing.T) {
	tests := []struct {
		name      string
		authToken string
		header    string
		query     string // ?token= value
		want      Tier
	}{
		{"no token configured", "", "", "", TierFull},
		{"no token configured, bearer sent", "", "Bearer xyz", "", TierFull},
		{"correct bearer", "secret", "Bearer secret", "", TierFull},
		{"wrong bearer", "secret", "Bearer wrong", "", TierPublic},
		{"missing bearer", "secret", "", "", TierPublic},
		{"raw token no prefix", "secret", "secret", "", TierFull},
		{"correct query param", "secret", "", "secret", TierFull},
		{"wrong query param", "secret", "", "wrong", TierPublic},
		{"header takes precedence over query", "secret", "Bearer secret", "wrong", TierFull},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/"
			if tt.query != "" {
				url = "/?token=" + tt.query
			}
			r, _ := http.NewRequest("GET", url, nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			if got := ResolveTier(tt.authToken, r); got != tt.want {
				t.Errorf("ResolveTier(%q, header=%q, query=%q) = %d, want %d",
					tt.authToken, tt.header, tt.query, got, tt.want)
			}
		})
	}
}
