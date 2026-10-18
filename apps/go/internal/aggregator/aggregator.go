// Package aggregator defines the vendor-neutral contract that every DEX aggregator client implements.
// Callers (the prober) depend only on this package, never on a vendor package.
package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Asset identifies a token on a chain. Chain is the lab's chain key (e.g. "base", "ethereum");
// Token is a contract address, mint address or the native-token placeholder of that chain.
type Asset struct {
	Chain string `yaml:"chain"`
	Token string `yaml:"token"`
}

// QuoteRequest asks for the best route to swap Amount (smallest units) of From into To.
type QuoteRequest struct {
	From        Asset   `yaml:"from"`
	To          Asset   `yaml:"to"`
	Amount      string  `yaml:"amount"`
	Address     string  `yaml:"address"`
	ToAddress   string  `yaml:"toAddress"`
	SlippagePct float64 `yaml:"slippagePct"`
}

// Quote is the best route returned by an aggregator, reduced to what the lab measures.
type Quote struct {
	Provider       string    // provider of the best route's first step
	Routes         int       // number of routes offered
	Steps          int       // steps in the best route
	AmountOut      string    // expected output of the best route, smallest units
	MinOut         string    // guaranteed minimum output after slippage, smallest units
	PriceImpactPct float64   // as reported by the aggregator
	ExpiresAt      time.Time // zero if not reported
	StepRefs       []string  // opaque references for BuildTx
}

// UnsignedTx is a transaction built by the aggregator. Payloads stay raw: nothing in the
// prober decodes them for signing.
type UnsignedTx struct {
	Chain   string
	Payload json.RawMessage
	Approve json.RawMessage
	Revoke  json.RawMessage
}

// Quoter is implemented by each aggregator client.
type Quoter interface {
	Name() string
	Supports(ctx context.Context, chain string) (bool, error)
	Quote(ctx context.Context, r QuoteRequest) (*Quote, error)
	BuildTx(ctx context.Context, stepRef string) (*UnsignedTx, error)
}

// Sentinel errors that implementations wrap, so callers can classify failures without vendor knowledge.
var (
	ErrRateLimited = errors.New("rate limited")
	ErrUnsupported = errors.New("unsupported")
	ErrNoRoute     = errors.New("no route")
	ErrDecode      = errors.New("decode response")
	// ErrThrottled means the client's own rate limiter could not grant a request before the deadline.
	ErrThrottled = errors.New("client-side rate limit wait exceeded deadline")
)

// HTTPError carries a non-2xx status from the aggregator.
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("aggregator returned HTTP %d", e.Status)
}
