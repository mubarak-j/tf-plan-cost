// Package pricingtest provides PriceClient fakes for tests.
package pricingtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// Client is a fake PriceClient backed by a map of Query.Key() -> price.
// A query with no entry fails loudly (it is a test bug, not "no price"),
// unless it was registered with SetNoPrice.
type Client struct {
	mu      sync.Mutex
	prices  map[string]pricing.UnitPrice
	noPrice map[string]bool
	calls   []pricing.Query
}

// New returns an empty fake.
func New() *Client {
	return &Client{prices: map[string]pricing.UnitPrice{}, noPrice: map[string]bool{}}
}

// Set registers a price for q.
func (c *Client) Set(q pricing.Query, usd, unit string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prices[q.Key()] = pricing.UnitPrice{USD: decimal.RequireFromString(usd), Unit: unit}
	return c
}

// SetNoPrice makes q return pricing.ErrNoPrice.
func (c *Client) SetNoPrice(q pricing.Query) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.prices, q.Key())
	c.noPrice[q.Key()] = true
	return c
}

// Lookup implements pricing.PriceClient.
func (c *Client) Lookup(_ context.Context, q pricing.Query) (pricing.UnitPrice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, q)
	key := q.Key()
	if p, ok := c.prices[key]; ok {
		return p, nil
	}
	if c.noPrice[key] {
		return pricing.UnitPrice{}, fmt.Errorf("%w for %s", pricing.ErrNoPrice, key)
	}
	return pricing.UnitPrice{}, fmt.Errorf("pricingtest: no price registered for %s", key)
}

// Calls returns the queries made so far.
func (c *Client) Calls() []pricing.Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pricing.Query(nil), c.calls...)
}

// Load reads prices recorded with Recorder.Save.
func Load(path string) (*Client, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := New()
	if err := json.Unmarshal(data, &c.prices); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Recorder wraps a real PriceClient and remembers successful lookups so they
// can be saved as test fixtures.
type Recorder struct {
	Next pricing.PriceClient

	mu     sync.Mutex
	prices map[string]pricing.UnitPrice
}

// Lookup implements pricing.PriceClient.
func (r *Recorder) Lookup(ctx context.Context, q pricing.Query) (pricing.UnitPrice, error) {
	p, err := r.Next.Lookup(ctx, q)
	if err == nil {
		r.mu.Lock()
		if r.prices == nil {
			r.prices = map[string]pricing.UnitPrice{}
		}
		r.prices[q.Key()] = p
		r.mu.Unlock()
	}
	return p, err
}

// Save writes the recorded prices as sorted, indented JSON.
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.MarshalIndent(r.prices, "", "  ") // map keys are sorted
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
