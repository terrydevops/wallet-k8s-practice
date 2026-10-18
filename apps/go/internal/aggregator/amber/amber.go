// Package amber implements aggregator.Quoter for the Amber swap API.
//
// The lab's chain keys are used as Amber domain IDs unchanged ("ethereum", "base", "solana", ...).
// Wire types follow the public API documentation and stay unexported.
package amber

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/trustwallet/go-libs/client"
	"golang.org/x/time/rate"

	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator"
	"github.com/terrydevops/wallet-k8s-practice/apps/go/internal/aggregator/amber/sign"
)

const (
	pathDomains = "/amber-api/v1/domains"
	pathRoute   = "/amber-api/v1/route"
	pathStep    = "/amber-api/v1/route/step"

	domainsTTL = time.Hour
)

// Config configures the client. RPS is the client-wide request budget.
type Config struct {
	BaseURL     string
	Credentials sign.Credentials
	RPS         float64
	Timeout     time.Duration
}

// Client talks to the Amber API. It is safe for concurrent use.
type Client struct {
	http    client.Request
	signer  *sign.Signer
	limiter *rate.Limiter

	mu        sync.Mutex
	domains   map[string]bool
	domainsAt time.Time
}

var _ aggregator.Quoter = (*Client)(nil)

// New builds a client. reg may be nil to disable the go-libs HTTP client metrics.
func New(cfg Config, reg prometheus.Registerer) (*Client, error) {
	if cfg.BaseURL == "" || cfg.Credentials.AccessID == "" || cfg.Credentials.Secret == "" {
		return nil, errors.New("amber: base URL and credentials are required")
	}
	if cfg.RPS <= 0 {
		return nil, errors.New("amber: RPS must be positive")
	}
	opts := []client.Option{client.TimeoutOption(cfg.Timeout)}
	if reg != nil {
		opts = append(opts, client.WithMetricsEnabled(reg, prometheus.Labels{"aggregator": "amber"}))
	}
	return &Client{
		http:    client.InitJSONClient(cfg.BaseURL, nil, opts...),
		signer:  sign.New(cfg.Credentials),
		limiter: rate.NewLimiter(rate.Limit(cfg.RPS), 1),
	}, nil
}

func (c *Client) Name() string { return "amber" }

// Supports reports whether Amber lists chain as a domain. The list is cached for an hour.
func (c *Client) Supports(ctx context.Context, chain string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.domains == nil || time.Since(c.domainsAt) > domainsTTL {
		var resp domainsResponse
		if err := c.do(ctx, http.MethodGet, pathDomains, nil, nil, &resp, "domains"); err != nil {
			return false, err
		}
		c.domains = make(map[string]bool, len(resp.Domains))
		for _, d := range resp.Domains {
			c.domains[d.ID] = true
		}
		c.domainsAt = time.Now()
	}
	return c.domains[chain], nil
}

// Quote returns the best route; Amber sorts routes by output, so the first one is the best.
func (c *Client) Quote(ctx context.Context, r aggregator.QuoteRequest) (*aggregator.Quote, error) {
	var resp routeResponse
	if err := c.do(ctx, http.MethodPost, pathRoute, nil, toRouteRequest(r), &resp, "route"); err != nil {
		return nil, err
	}
	return fromRouteResponse(resp)
}

// BuildTx returns the unsigned transaction(s) for one step of a route.
func (c *Client) BuildTx(ctx context.Context, stepRef string) (*aggregator.UnsignedTx, error) {
	var resp stepResponse
	if err := c.do(ctx, http.MethodPost, pathStep, nil, stepRequest{StepID: stepRef}, &resp, "route_step"); err != nil {
		return nil, err
	}
	var head struct {
		DomainID string `json:"domainId"`
	}
	if err := json.Unmarshal(resp.Transaction, &head); err != nil {
		return nil, fmt.Errorf("%w: transaction: %w", aggregator.ErrDecode, err)
	}
	return &aggregator.UnsignedTx{Chain: head.DomainID, Payload: resp.Transaction, Approve: resp.Approve, Revoke: resp.RevokeApproval}, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any, metric string) error {
	if err := c.limiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %w", aggregator.ErrThrottled, err)
	}
	b := client.NewReqBuilder().
		Method(method).
		PathStatic(path).
		Query(q).
		Headers(c.signer.Headers(method, path, q)).
		WriteTo(out).
		MetricName(metric)
	if body != nil {
		b = b.Body(body)
	}
	_, err := c.http.Execute(ctx, b.Build())
	return mapError(err)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *client.HttpError
	if errors.As(err, &httpErr) {
		e := &aggregator.HTTPError{Status: httpErr.StatusCode}
		if httpErr.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("%w: %w", aggregator.ErrRateLimited, e)
		}
		return e
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return fmt.Errorf("%w: %w", aggregator.ErrDecode, err)
	}
	return err
}

// --- wire types and mapping -------------------------------------------------------------------

type routeRequest struct {
	FromAsset    string `json:"fromAsset"`
	FromAddress  string `json:"fromAddress"`
	FromDomain   string `json:"fromDomain"`
	Amount       string `json:"amount"`
	ToAsset      string `json:"toAsset"`
	ToAddress    string `json:"toAddress,omitempty"`
	ToDomain     string `json:"toDomain"`
	Slippage     string `json:"slippage,omitempty"`
	SortBy       string `json:"sortBy"`
	ContractCall bool   `json:"contractCall"`
}

type routeResponse struct {
	Routes []struct {
		ID string `json:"id"`
		// Kept as a string: the field is informational, so an unexpected format must not fail the quote.
		ExpirationDate string `json:"expirationDate"`
		Steps          []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			To   struct {
				Amount       string `json:"amount"`
				MinAmountOut string `json:"minAmountOut"`
			} `json:"to"`
			PriceImpact string `json:"priceImpact"`
			Provider    struct {
				ID string `json:"id"`
			} `json:"provider"`
		} `json:"steps"`
	} `json:"routes"`
}

type stepRequest struct {
	StepID string `json:"stepId"`
}

type stepResponse struct {
	StepID         string          `json:"stepId"`
	Transaction    json.RawMessage `json:"transaction"`
	Approve        json.RawMessage `json:"approve"`
	RevokeApproval json.RawMessage `json:"revokeApproval"`
}

type domainsResponse struct {
	Domains []struct {
		ID string `json:"id"`
	} `json:"domains"`
}

func toRouteRequest(r aggregator.QuoteRequest) routeRequest {
	w := routeRequest{
		FromAsset:   r.From.Token,
		FromAddress: r.Address,
		FromDomain:  r.From.Chain,
		Amount:      r.Amount,
		ToAsset:     r.To.Token,
		ToAddress:   r.ToAddress,
		ToDomain:    r.To.Chain,
		SortBy:      "outcome",
	}
	if r.SlippagePct > 0 {
		w.Slippage = strconv.FormatFloat(r.SlippagePct, 'f', -1, 64)
	}
	return w
}

func fromRouteResponse(resp routeResponse) (*aggregator.Quote, error) {
	if len(resp.Routes) == 0 || len(resp.Routes[0].Steps) == 0 {
		return nil, aggregator.ErrNoRoute
	}
	best := resp.Routes[0]
	first, last := best.Steps[0], best.Steps[len(best.Steps)-1]
	q := &aggregator.Quote{
		Provider:  first.Provider.ID,
		Routes:    len(resp.Routes),
		Steps:     len(best.Steps),
		AmountOut: last.To.Amount,
		MinOut:    last.To.MinAmountOut,
	}
	if t, err := time.Parse(time.RFC3339, best.ExpirationDate); err == nil {
		q.ExpiresAt = t
	}
	if first.PriceImpact != "" {
		v, err := strconv.ParseFloat(first.PriceImpact, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: priceImpact %q", aggregator.ErrDecode, first.PriceImpact)
		}
		q.PriceImpactPct = v
	}
	for _, s := range best.Steps {
		q.StepRefs = append(q.StepRefs, s.ID)
	}
	return q, nil
}
