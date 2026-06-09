package cmd

import (
	"fmt"
	"sync"
	"time"
)

// ttlCache is a tiny process-wide cache with a TTL and a hard size cap. It
// exists so the HTTP server doesn't re-issue the same upstream forecast
// requests when a user flips quickly between pages (rain → hourly → 14-day →
// today → scout) within a few minutes — the scout and today fan-outs alone are
// 100+ Open-Meteo calls each, so re-running them on every navigation is the
// expensive case this guards against.
//
// Values are shared (pointers/slices), so cached results MUST be treated as
// read-only by callers. The CLI gets a fresh empty cache per process, so it
// only ever dedupes within a single command run.
type ttlCache[T any] struct {
	mu  sync.Mutex
	m   map[string]ttlEntry[T]
	ttl time.Duration
	max int
}

type ttlEntry[T any] struct {
	val T
	exp time.Time
}

func newTTLCache[T any](ttl time.Duration, max int) *ttlCache[T] {
	return &ttlCache[T]{m: make(map[string]ttlEntry[T]), ttl: ttl, max: max}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.exp) {
		if ok {
			delete(c.m, key)
		}
		var zero T
		return zero, false
	}
	return e.val, true
}

func (c *ttlCache[T]) put(key string, v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		c.evictLocked()
	}
	c.m[key] = ttlEntry[T]{val: v, exp: time.Now().Add(c.ttl)}
}

// evictLocked drops expired entries first; if that still leaves the map at the
// cap it drops arbitrary entries (map order is random) until under it. Caller
// holds the lock.
func (c *ttlCache[T]) evictLocked() {
	now := time.Now()
	for k, e := range c.m {
		if now.After(e.exp) {
			delete(c.m, k)
		}
	}
	for k := range c.m {
		if len(c.m) < c.max {
			break
		}
		delete(c.m, k)
	}
}

// memo returns the cached value for key, or runs fn and caches a successful
// result. Errors are never cached, so a transient upstream failure doesn't
// pin a bad result for the whole TTL.
func memo[T any](c *ttlCache[T], key string, fn func() (T, error)) (T, error) {
	if v, ok := c.get(key); ok {
		return v, nil
	}
	v, err := fn()
	if err == nil {
		c.put(key, v)
	}
	return v, err
}

// Per-upstream caches. TTLs reflect how fast each source changes:
//   - nowcast (Buienalarm/Buienradar): a fast-moving 2 h precipitation line,
//     so a short window — long enough to absorb page-flipping, short enough to
//     stay current.
//   - Open-Meteo hourly: the model refreshes roughly hourly.
//   - Open-Meteo daily: refreshes a few times a day.
//
// Size caps bound memory under the scout/today fan-out (many distinct points).
var (
	buienalarmCache     = newTTLCache[*Forecast](2*time.Minute, 512)
	buineradarCache     = newTTLCache[*Forecast](2*time.Minute, 512)
	openMeteoRangeCache = newTTLCache[*OpenMeteoData](10*time.Minute, 4096)
	openMeteoDailyCache = newTTLCache[[]DailyAggregate](30*time.Minute, 512)
)

// locationZones remembers the timezone Open-Meteo reported per rounded
// coordinate, so pre-fetch UI — streamed page heads and HH:MM clock inputs —
// can use the location's wall clock instead of the server's. The streaming
// design flushes headers before any upstream call, so the first visit for an
// area can only guess (server zone); every later view is exact.
var locationZones sync.Map // "lat|lon" (1 decimal ≈ 11 km) -> *time.Location

func zoneKey(lat, lon float64) string { return fmt.Sprintf("%.1f|%.1f", lat, lon) }

func rememberZone(lat, lon float64, zone *time.Location) {
	if zone != time.Local {
		locationZones.Store(zoneKey(lat, lon), zone)
	}
}

// locationZone returns the best-known timezone for the coordinates: the one
// Open-Meteo reported for a nearby point earlier in this process, else the
// server's own zone.
func locationZone(lat, lon float64) *time.Location {
	if z, ok := locationZones.Load(zoneKey(lat, lon)); ok {
		return z.(*time.Location)
	}
	return time.Local
}
