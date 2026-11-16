package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/time/rate"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

// Every public caller draws on the same upstream allowances: one
// OpenAlex credit budget (~10,000 a day, ~20-30 per search) and the
// contact email's standing with Crossref and Wikimedia. Without a
// per-caller cap, one agent stuck in a loop empties the day's budget
// for everyone. The cap is generous for a person working through a
// question with a model and tight for a runaway loop: a burst of 40
// calls, then one more every 90 seconds (about 40 an hour sustained).
// Full-tier callers are not limited.
const (
	publicTierCallBurstPerClient          = 40
	publicTierCallRefillIntervalPerClient = 90 * time.Second
	publicTierIdleClientForgottenAfter    = 2 * time.Hour
)

type clientAddressContextKey struct{}

func withClientAddress(ctx context.Context, clientAddress string) context.Context {
	return context.WithValue(ctx, clientAddressContextKey{}, clientAddress)
}

func clientAddressFromContext(ctx context.Context) string {
	clientAddress, _ := ctx.Value(clientAddressContextKey{}).(string)
	return clientAddress
}

// clientAddressOfRequest names the caller. In production the server
// listens on localhost behind Caddy, which (with no trusted_proxies
// configured) discards any client-supplied X-Forwarded-For and appends
// the real peer address, so the rightmost entry is the one Caddy wrote
// and cannot be spoofed. Without the header, the socket address is used.
func clientAddressOfRequest(r *http.Request) string {
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		entries := strings.Split(forwardedFor, ",")
		if rightmost := strings.TrimSpace(entries[len(entries)-1]); rightmost != "" {
			return rightmost
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type clientCallAllowance struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// publicTierCallLimiterByClientAddress holds one token bucket per
// client address and forgets addresses that have gone quiet, so the
// map does not grow without bound.
type publicTierCallLimiterByClientAddress struct {
	mutex          sync.Mutex
	allowances     map[string]*clientCallAllowance
	burst          int
	refillInterval time.Duration
	forgetAfter    time.Duration
	lastSweep      time.Time
}

func newPublicTierCallLimiterByClientAddress(burst int, refillInterval, forgetAfter time.Duration) *publicTierCallLimiterByClientAddress {
	return &publicTierCallLimiterByClientAddress{
		allowances:     make(map[string]*clientCallAllowance),
		burst:          burst,
		refillInterval: refillInterval,
		forgetAfter:    forgetAfter,
	}
}

// allow spends one call from the client's allowance. When the
// allowance is empty it spends nothing and reports how long until the
// next call would be allowed.
func (l *publicTierCallLimiterByClientAddress) allow(clientAddress string, now time.Time) (bool, time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if now.Sub(l.lastSweep) > l.forgetAfter {
		for address, allowance := range l.allowances {
			if now.Sub(allowance.lastSeen) > l.forgetAfter {
				delete(l.allowances, address)
			}
		}
		l.lastSweep = now
	}

	allowance, known := l.allowances[clientAddress]
	if !known {
		allowance = &clientCallAllowance{limiter: rate.NewLimiter(rate.Every(l.refillInterval), l.burst)}
		l.allowances[clientAddress] = allowance
	}
	allowance.lastSeen = now

	reservation := allowance.limiter.ReserveN(now, 1)
	if wait := reservation.DelayFrom(now); wait > 0 {
		reservation.CancelAt(now)
		return false, wait
	}
	return true, 0
}

// publicTierCallLimitedHandler refuses public-tier calls from a client
// that has used up its allowance. The refusal is a tool error rather
// than a protocol error, so the model reads it and can tell the person
// why, instead of the client reporting an opaque failure.
func publicTierCallLimitedHandler(name string, limiter *publicTierCallLimiterByClientAddress, handler tools.HandlerFunc) tools.HandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if auth.GetTier(ctx) != auth.TierPublic {
			return handler(ctx, request)
		}
		allowed, wait := limiter.allow(clientAddressFromContext(ctx), time.Now())
		if allowed {
			return handler(ctx, request)
		}
		log.Printf("tool=%s tier=%s outcome=rate-limited retry_after=%s",
			name, auth.GetTier(ctx), wait.Truncate(time.Second))
		return mcp.NewToolResultError(fmt.Sprintf(
			"This is a shared public server, and each caller gets about %d tool calls an hour so that one busy session can't use up the day's database allowance for everyone. This caller has reached that limit. The next call will be allowed in about %s. Tell the person this, and carry on without further tool calls until then.",
			int(time.Hour/publicTierCallRefillIntervalPerClient), humaniseWait(wait))), nil
	}
}

func humaniseWait(wait time.Duration) string {
	if wait < time.Minute {
		return "a minute"
	}
	minutes := int((wait + time.Minute - 1) / time.Minute)
	if minutes == 1 {
		return "a minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
