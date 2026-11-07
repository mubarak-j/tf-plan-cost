package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultCacheTTL is how long a cached price is trusted. AWS list prices
// change rarely; a week keeps API calls low without going stale.
const DefaultCacheTTL = 7 * 24 * time.Hour

// DefaultCacheDir returns the per-user cache directory for prices.
func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tf-plan-cost", "prices"), nil
}

// Cache is a PriceClient decorator that caches successful lookups in memory
// and, if Dir is set, on disk. Errors are never cached.
type Cache struct {
	next PriceClient
	dir  string
	ttl  time.Duration
	now  func() time.Time

	mu  sync.Mutex
	mem map[string]UnitPrice
}

// NewCache wraps next. An empty dir disables the disk layer.
func NewCache(next PriceClient, dir string, ttl time.Duration) *Cache {
	return &Cache{next: next, dir: dir, ttl: ttl, now: time.Now, mem: map[string]UnitPrice{}}
}

type cacheEntry struct {
	Key   string    `json:"key"`
	Price UnitPrice `json:"price"`
}

// Lookup implements PriceClient.
func (c *Cache) Lookup(ctx context.Context, q Query) (UnitPrice, error) {
	key := q.Key()

	c.mu.Lock()
	p, ok := c.mem[key]
	c.mu.Unlock()
	if ok && c.fresh(p) {
		return p, nil
	}

	if p, ok := c.readDisk(key); ok {
		c.store(key, p)
		return p, nil
	}

	p, err := c.next.Lookup(ctx, q)
	if err != nil {
		return UnitPrice{}, err
	}
	if p.FetchedAt.IsZero() {
		p.FetchedAt = c.now().UTC()
	}
	c.store(key, p)
	// The disk cache is an optimization; a read-only or full disk must not
	// fail the estimate.
	_ = c.writeDisk(key, p)
	return p, nil
}

func (c *Cache) fresh(p UnitPrice) bool {
	return c.now().Sub(p.FetchedAt) < c.ttl
}

func (c *Cache) store(key string, p UnitPrice) {
	c.mu.Lock()
	c.mem[key] = p
	c.mu.Unlock()
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

func (c *Cache) readDisk(key string) (UnitPrice, bool) {
	if c.dir == "" {
		return UnitPrice{}, false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return UnitPrice{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil || e.Key != key || !c.fresh(e.Price) {
		return UnitPrice{}, false
	}
	return e.Price, true
}

func (c *Cache) writeDisk(key string, p UnitPrice) error {
	if c.dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cacheEntry{Key: key, Price: p})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, "*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), c.path(key)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write price cache: %w", err)
	}
	return nil
}
