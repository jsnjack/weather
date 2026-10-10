package cmd

import (
	"fmt"
	"sync"
	"time"
)

// ttlCache bounds memory and shares concurrent fetches for the same key.
// Cached values must be treated as read-only.
type ttlCache[T any] struct {
	mu      sync.Mutex
	m       map[string]ttlEntry[T]
	flights map[string]*cacheFlight[T]
	ttl     time.Duration
	max     int
	expires func(T) time.Time
}

type cacheFlight[T any] struct {
	done chan struct{}
	val  T
	err  error
}

type ttlEntry[T any] struct {
	val T
	exp time.Time
}

func newTTLCache[T any](ttl time.Duration, max int) *ttlCache[T] {
	return &ttlCache[T]{m: make(map[string]ttlEntry[T]), flights: make(map[string]*cacheFlight[T]), ttl: ttl, max: max}
}

func (c *ttlCache[T]) put(key string, v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		c.evictLocked()
	}
	expires := time.Now().Add(c.ttl)
	if c.expires != nil {
		expires = c.expires(v)
	}
	c.m[key] = ttlEntry[T]{val: v, exp: expires}
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
	c.mu.Lock()
	if entry, ok := c.m[key]; ok && time.Now().Before(entry.exp) {
		c.mu.Unlock()
		return entry.val, nil
	}
	if flight, ok := c.flights[key]; ok {
		c.mu.Unlock()
		<-flight.done
		return flight.val, flight.err
	}
	flight := &cacheFlight[T]{done: make(chan struct{})}
	c.flights[key] = flight
	c.mu.Unlock()
	flight.val, flight.err = fn()
	if flight.err == nil {
		c.put(key, flight.val)
	}
	c.mu.Lock()
	delete(c.flights, key)
	close(flight.done)
	c.mu.Unlock()
	return flight.val, flight.err
}

var (
	buienalarmCache = newTTLCache[*Forecast](2*time.Minute, 512)
	buineradarCache = newTTLCache[*Forecast](2*time.Minute, 512)
)

const (
	openMeteoHourlyTTL = 10 * time.Minute
	openMeteoDailyTTL  = 30 * time.Minute
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
