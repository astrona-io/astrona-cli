package localdata

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultRetentionDays is the maximum age of disposable cache entries.
	DefaultRetentionDays = 30
	// DefaultMaxSizeGB is the combined disposable-cache budget, in decimal GB.
	DefaultMaxSizeGB = 50
	// BytesPerGB defines the unit used by cache-max-size-gb.
	BytesPerGB int64 = 1_000_000_000
)

// CachePolicy evicts expired entries, then the oldest remaining entries until
// their combined logical size fits MaxBytes. Zero MaxBytes means no size cap.
type CachePolicy struct {
	MaxAge   time.Duration
	MaxBytes int64
}

type cacheGroup struct {
	key      string
	items    []Item
	bytes    int64
	modified time.Time
}

// PlanPolicy applies limits only to the disposable entries allowed by PlanClean.
// Saved lab state defers all eviction. Callers applying the plan must hold the
// exclusive maintenance lock from planning through deletion.
func (s Store) PlanPolicy(ctx context.Context, policy CachePolicy, now time.Time) (Plan, error) {
	if policy.MaxAge <= 0 || policy.MaxBytes < 0 {
		return Plan{}, fmt.Errorf("cache retention must be positive and size limit must not be negative")
	}
	p, err := s.PlanClean(ctx, false, now)
	if err != nil {
		return p, err
	}
	groups := map[string]*cacheGroup{}
	var total int64
	for _, item := range p.Items {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if item.Bytes < 0 || item.Bytes > math.MaxInt64-total {
			return p, fmt.Errorf("combined cache size exceeds supported range")
		}
		total += item.Bytes
		key := item.Path
		if item.base == filepath.Join(s.Data, "cache", "images") {
			// Verified images use x.qcow2.meta.json; unverified images use
			// x.meta.json. Evict each image and its sidecar together.
			stem := strings.TrimSuffix(strings.TrimSuffix(item.name, ".meta.json"), ".qcow2")
			key = filepath.Join(item.base, stem)
		}
		g := groups[key]
		if g == nil {
			g = &cacheGroup{key: key}
			groups[key] = g
		}
		g.items = append(g.items, item)
		g.bytes += item.Bytes
		if item.modified.After(g.modified) {
			g.modified = item.modified
		}
	}
	ordered := make([]*cacheGroup, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].modified.Equal(ordered[j].modified) {
			return ordered[i].key < ordered[j].key
		}
		return ordered[i].modified.Before(ordered[j].modified)
	})
	p.Items = []Item{}
	cutoff := now.Add(-policy.MaxAge)
	for _, g := range ordered {
		if !g.modified.After(cutoff) || (policy.MaxBytes > 0 && total > policy.MaxBytes) {
			p.Items = append(p.Items, g.items...)
			total -= g.bytes
		}
	}
	return p, nil
}
