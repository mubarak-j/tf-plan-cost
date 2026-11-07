package pricing

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type countingClient struct {
	calls int
	price UnitPrice
	err   error
}

func (c *countingClient) Lookup(context.Context, Query) (UnitPrice, error) {
	c.calls++
	return c.price, c.err
}

func TestCache(t *testing.T) {
	ctx := context.Background()
	q := Query{ServiceCode: "AmazonEC2", Filters: map[string]string{"instanceType": "m6i.large"}}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()

	next := &countingClient{price: UnitPrice{USD: decimal.RequireFromString("0.096"), Unit: "Hrs", FetchedAt: now}}
	c := NewCache(next, dir, time.Hour)
	c.now = func() time.Time { return now }

	for range 3 {
		p, err := c.Lookup(ctx, q)
		if err != nil || p.USD.String() != "0.096" {
			t.Fatalf("Lookup = %v, %v", p, err)
		}
	}
	if next.calls != 1 {
		t.Errorf("memory cache: next called %d times, want 1", next.calls)
	}

	// A new cache on the same dir reads from disk.
	c2 := NewCache(next, dir, time.Hour)
	c2.now = func() time.Time { return now.Add(30 * time.Minute) }
	p, err := c2.Lookup(ctx, q)
	if err != nil || p.USD.String() != "0.096" || !p.FetchedAt.Equal(now) {
		t.Fatalf("disk Lookup = %+v, %v", p, err)
	}
	if next.calls != 1 {
		t.Errorf("disk cache: next called %d times, want 1", next.calls)
	}

	// Expired entries are refetched.
	c3 := NewCache(next, dir, time.Hour)
	c3.now = func() time.Time { return now.Add(2 * time.Hour) }
	next.price.FetchedAt = now.Add(2 * time.Hour)
	if _, err := c3.Lookup(ctx, q); err != nil {
		t.Fatal(err)
	}
	if next.calls != 2 {
		t.Errorf("expired: next called %d times, want 2", next.calls)
	}
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	next := &countingClient{err: errors.New("throttled")}
	c := NewCache(next, "", time.Hour)
	for range 2 {
		if _, err := c.Lookup(context.Background(), Query{}); err == nil {
			t.Fatal("want error")
		}
	}
	if next.calls != 2 {
		t.Errorf("next called %d times, want 2", next.calls)
	}
}

func TestCacheIgnoresUnwritableDir(t *testing.T) {
	file := t.TempDir() + "/not-a-dir"
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	next := &countingClient{price: UnitPrice{USD: decimal.NewFromInt(1)}}
	c := NewCache(next, file, time.Hour)
	if _, err := c.Lookup(context.Background(), Query{}); err != nil {
		t.Fatalf("Lookup with unwritable cache dir: %v", err)
	}
}
