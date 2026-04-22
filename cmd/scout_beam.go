package cmd

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// beamNode is one partial or complete trip plan evaluated by the beam search.
type beamNode struct {
	Bearings    []float64    // day-by-day compass bearing (len == depth so far)
	Positions   []latLon     // cumulative positions — Positions[0] is start, len == depth+1
	DailyScores []DayScore   // per-day scored details for rendering
	Score       float64      // cumulative score after all penalties
}

type latLon struct {
	Lat, Lon float64
}

// beamConfig bundles user-tunable knobs for the search.
type beamConfig struct {
	KmPerDay          float64
	MinTemp           float64
	BeamWidth         int
	PivotPenalty      float64 // subtracted per bearing change
	RoundTrip         bool
	RoundTripPenalty  float64 // subtracted per km from start at trip end (only when RoundTrip)
}

// hourlyCache dedupes Open-Meteo fetches. Two paths that arrive at the same
// midpoint on the same day share one HTTP call.
type hourlyCache struct {
	mu   sync.Mutex
	data map[string]*OpenMeteoData
}

func newHourlyCache() *hourlyCache {
	return &hourlyCache{data: make(map[string]*OpenMeteoData)}
}

func hourlyCacheKey(lat, lon float64, date time.Time) string {
	return fmt.Sprintf("%.2f|%.2f|%s", lat, lon, date.Format("2006-01-02"))
}

// prefetch fills the cache with every (lat, lon, date) triple in points,
// using a bounded worker pool. Errors on individual points are logged but
// don't abort the batch — we just won't have data for that candidate.
func (c *hourlyCache) prefetch(points []fetchPoint) {
	sem := make(chan struct{}, scoutFetchWorkers)
	var wg sync.WaitGroup
	for _, p := range points {
		key := hourlyCacheKey(p.Lat, p.Lon, p.Date)
		c.mu.Lock()
		if _, ok := c.data[key]; ok {
			c.mu.Unlock()
			continue
		}
		// Placeholder so concurrent prefetches don't double-fetch.
		c.data[key] = nil
		c.mu.Unlock()

		wg.Add(1)
		sem <- struct{}{}
		go func(p fetchPoint, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			data, err := GetOpenMeteoRange(p.Lat, p.Lon, p.Date, p.Date)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err != nil {
				DebugLogger.Printf("scout: fetch failed (%.2f,%.2f %s): %s\n", p.Lat, p.Lon, p.Date.Format("2006-01-02"), err)
				delete(c.data, key) // leave as cache miss; get() returns error
				return
			}
			c.data[key] = data
		}(p, key)
	}
	wg.Wait()
}

func (c *hourlyCache) get(lat, lon float64, date time.Time) (*OpenMeteoData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.data[hourlyCacheKey(lat, lon, date)]
	return data, ok && data != nil
}

type fetchPoint struct {
	Lat, Lon float64
	Date     time.Time
}

// RunBeamSearch expands the position tree day by day, keeping only the top
// BeamWidth surviving paths at each depth. Returns all surviving final-day
// paths, sorted by score descending. An empty result means every candidate
// was disqualified (e.g. rain in every direction on some day).
func RunBeamSearch(startLat, startLon float64, startDate time.Time, days int, cfg beamConfig) []beamNode {
	cache := newHourlyCache()
	start := latLon{startLat, startLon}
	beam := []beamNode{{
		Positions: []latLon{start},
	}}

	for day := 0; day < days; day++ {
		date := startDate.AddDate(0, 0, day)

		// Phase 1: collect unique fetch points (midpoints of this day's legs).
		uniq := map[string]fetchPoint{}
		for _, node := range beam {
			cur := node.Positions[len(node.Positions)-1]
			for i := 0; i < scoutNumDirections; i++ {
				bearing := float64(i) * (360.0 / scoutNumDirections)
				midLat, midLon := DestinationPoint(cur.Lat, cur.Lon, bearing, cfg.KmPerDay/2)
				key := hourlyCacheKey(midLat, midLon, date)
				uniq[key] = fetchPoint{Lat: midLat, Lon: midLon, Date: date}
			}
		}
		points := make([]fetchPoint, 0, len(uniq))
		for _, p := range uniq {
			points = append(points, p)
		}
		DebugLogger.Printf("scout: day %d — %d unique fetches from %d beam nodes\n", day+1, len(points), len(beam))
		cache.prefetch(points)

		// Phase 2: expand each beam node with 8 bearings; score the resulting leg.
		candidates := make([]beamNode, 0, len(beam)*scoutNumDirections)
		for _, node := range beam {
			cur := node.Positions[len(node.Positions)-1]
			for i := 0; i < scoutNumDirections; i++ {
				bearing := float64(i) * (360.0 / scoutNumDirections)
				midLat, midLon := DestinationPoint(cur.Lat, cur.Lon, bearing, cfg.KmPerDay/2)
				data, ok := cache.get(midLat, midLon, date)
				if !ok {
					continue
				}
				// Skip bearings whose day-midpoint lands on water — you can't
				// cycle across the North Sea or IJsselmeer.
				if data.IsSea() {
					continue
				}
				ds := ScoreDay(data.Hourly, bearing, cfg.MinTemp)
				if ds.Disqualified {
					continue
				}

				endLat, endLon := DestinationPoint(cur.Lat, cur.Lon, bearing, cfg.KmPerDay)
				pivot := 0.0
				if len(node.Bearings) > 0 && node.Bearings[len(node.Bearings)-1] != bearing {
					pivot = cfg.PivotPenalty
				}
				newScore := node.Score + ds.Score - pivot

				// Round-trip penalty is only applied on the last day — that's
				// when "how far from home do we finish" actually matters.
				if cfg.RoundTrip && day == days-1 {
					distKm := HaversineKm(endLat, endLon, startLat, startLon)
					newScore -= distKm * cfg.RoundTripPenalty / 100
				}

				newBearings := append(append([]float64{}, node.Bearings...), bearing)
				newPositions := append(append([]latLon{}, node.Positions...), latLon{endLat, endLon})
				newDaily := append(append([]DayScore{}, node.DailyScores...), ds)
				candidates = append(candidates, beamNode{
					Bearings:    newBearings,
					Positions:   newPositions,
					DailyScores: newDaily,
					Score:       newScore,
				})
			}
		}

		// Phase 3: prune to top BeamWidth by score.
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Score > candidates[j].Score
		})
		if len(candidates) > cfg.BeamWidth {
			candidates = candidates[:cfg.BeamWidth]
		}
		beam = candidates
		if len(beam) == 0 {
			return beam
		}
	}

	return beam
}
