package cmd

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	localRadarSize = 256
	// localRadarRadiusM is the default map radius; the widget always uses it.
	localRadarRadiusM = 50_000.0
	// Every KNMI request covers the widest zoom at twice the map resolution;
	// narrower maps are cut from its centre, so zooming costs no extra KNMI
	// requests. KNMI's grid is about 1 km, coarser than either map's pixels.
	localRadarSourceRadiusKM   = 100
	localRadarSourceSize       = 512
	radarEarthRadiusM          = 6_378_137.0
	localRadarMaxAge           = 20 * time.Minute
	knmiWMSURL                 = "https://anonymous.api.dataplatform.knmi.nl/wms/adaguc-server"
	radarAnonymousInterval     = time.Second
	radarAuthenticatedInterval = time.Second / 20
)

//go:embed radar/land.json
var radarLandFS embed.FS

var (
	radarObservationCache = newTTLCache[time.Time](3*time.Minute, 1)
	localRadarCache       = newTTLCache[*localRadarImage](3*time.Minute, 64)
	localRadarSourceCache = newTTLCache[image.Image](3*time.Minute, 32)
	radarLandOnce         sync.Once
	radarLand             [][][2]float64
	radarLandErr          error
	radarWMSGate          struct {
		sync.Mutex
		next, cooldown time.Time
	}
)

// localRadarRadiiKM are the map radii the web zoom offers.
var localRadarRadiiKM = []int{25, 50, 100}

type localRadarImage struct {
	overlay         image.Image
	background      image.Image
	labels          image.Image
	observed        time.Time
	x, y, halfWidth float64
	radiusKM        int
}

// shown reports whether the map is worth returning: fresh, and either rainy
// or zoomed. The default map needs rain inside its circle; the web offers
// zoom only after that map appeared, so a dry zoomed map is still useful.
func (r *localRadarImage) shown() bool {
	return r != nil && freshRadarObservation(r.observed, time.Now()) &&
		(r.radiusKM != int(localRadarRadiusM/1000) || hasLocalRadarRain(r.overlay))
}

// localRadarSourceBBox is the Web Mercator box of the KNMI request shared by
// every zoom of the map centred at x, y.
func localRadarSourceBBox(radar *localRadarImage) string {
	half := radar.halfWidth * localRadarSourceRadiusKM / float64(radar.radiusKM)
	return fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", radar.x-half, radar.y-half, radar.x+half, radar.y+half)
}

// cropLocalRadarOverlay cuts the map's radius from the centre of a source
// overlay and scales it to localRadarSize with nearest-neighbour sampling,
// so KNMI's rain classes keep their exact colours.
func cropLocalRadarOverlay(source image.Image, radiusKM int) image.Image {
	bounds := source.Bounds()
	span := float64(bounds.Dx()) * float64(radiusKM) / localRadarSourceRadiusKM
	offset := (float64(bounds.Dx()) - span) / 2
	result := image.NewNRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
	for y := range localRadarSize {
		sy := bounds.Min.Y + int(offset+(float64(y)+.5)*span/localRadarSize)
		for x := range localRadarSize {
			sx := bounds.Min.X + int(offset+(float64(x)+.5)*span/localRadarSize)
			result.Set(x, y, source.At(sx, sy))
		}
	}
	return result
}

func localRadarRadiusRequest(r *http.Request) (int, error) {
	value := r.URL.Query().Get("radius")
	if value == "" {
		return int(localRadarRadiusM / 1000), nil
	}
	for _, radius := range localRadarRadiiKM {
		if value == strconv.Itoa(radius) {
			return radius, nil
		}
	}
	return 0, fmt.Errorf("radar radius must be 25, 50 or 100 km")
}

func radarMercator(lat, lon float64) (float64, float64) {
	return radarEarthRadiusM * lon * math.Pi / 180, radarEarthRadiusM * math.Log(math.Tan(math.Pi/4+lat*math.Pi/360))
}

func radarWMSRequest(ctx context.Context, query url.Values) ([]byte, error) {
	endpoint := knmiWMSURL
	key := os.Getenv("KNMI_WMS_API_KEY")
	if key != "" {
		endpoint = "https://api.dataplatform.knmi.nl/wms/adaguc-server"
	}
	if query.Get("DATASET") == "" {
		query.Set("DATASET", "radar_reflectivity_composites")
	}
	query.Set("SERVICE", "WMS")
	requestURL := endpoint + "?" + query.Encode()
	cachePath, cacheErr := forecastStorePath("knmi-wms-v1:" + requestURL)
	if cacheErr != nil {
		slog.Debug("radar disk cache unavailable", "err", cacheErr)
	}
	if cacheErr == nil {
		if entry, ok := readStoredForecast(cachePath, time.Now()); ok {
			var cached []byte
			if err := json.Unmarshal(entry.Body, &cached); err == nil {
				return cached, nil
			}
		}
	}
	radarWMSGate.Lock()
	now := time.Now()
	if now.Before(radarWMSGate.cooldown) {
		radarWMSGate.Unlock()
		return nil, fmt.Errorf("KNMI radar requests are cooling down")
	}
	slot := radarWMSGate.next
	if slot.Before(now) {
		slot = now
	}
	interval := radarAnonymousInterval
	if key != "" {
		interval = radarAuthenticatedInterval
	}
	radarWMSGate.next = slot.Add(interval)
	radarWMSGate.Unlock()
	timer := time.NewTimer(time.Until(slot))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for KNMI radar: %w", ctx.Err())
	case <-timer.C:
	}
	radarWMSGate.Lock()
	coolingDown := time.Now().Before(radarWMSGate.cooldown)
	radarWMSGate.Unlock()
	if coolingDown {
		return nil, fmt.Errorf("KNMI radar requests are cooling down")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create KNMI radar request: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", key)
	}
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch KNMI radar: %w", err)
	}
	defer closeBody(response.Body, "KNMI WMS response")
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			radarWMSGate.Lock()
			radarWMSGate.cooldown = retryAfter(response.Header.Get("Retry-After"), time.Now())
			radarWMSGate.Unlock()
		}
		return nil, fmt.Errorf("KNMI radar HTTP %d", response.StatusCode)
	}
	const maxBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read KNMI radar: %w", err)
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("KNMI radar response exceeds size limit")
	}
	switch query.Get("REQUEST") {
	case "GetCapabilities":
		var err error
		if query.Get("DATASET") == radarForecastDataset {
			_, err = parseRadarDimensionTime(body, "precipitation_nowcast", "reference_time")
		} else {
			_, err = parseRadarObservationTime(body)
		}
		if err != nil {
			return nil, err
		}
	case "GetMap":
		config, err := png.DecodeConfig(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("read KNMI radar PNG: %w", err)
		}
		if strconv.Itoa(config.Width) != query.Get("WIDTH") || strconv.Itoa(config.Height) != query.Get("HEIGHT") {
			return nil, fmt.Errorf("unexpected KNMI radar image size")
		}
	}
	if cacheErr == nil {
		wrapped, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode radar cache: %w", err)
		}
		if err := writeStoredForecast(cachePath, storedForecast{Body: wrapped, Expires: time.Now().Add(3 * time.Minute)}); err != nil {
			slog.Debug("save radar disk cache", "err", err)
		}
	}
	return body, nil
}

func latestRadarTime(ctx context.Context) (time.Time, error) {
	return memo(radarObservationCache, "latest", func() (time.Time, error) {
		body, err := radarWMSRequest(ctx, url.Values{"REQUEST": {"GetCapabilities"}})
		if err != nil {
			return time.Time{}, err
		}
		return parseRadarObservationTime(body)
	})
}

func parseRadarObservationTime(body []byte) (time.Time, error) {
	return parseRadarDimensionTime(body, "precipitation", "time")
}

func parseRadarDimensionTime(body []byte, layerName, dimensionName string) (time.Time, error) {
	var capabilities struct {
		Capability struct {
			Layer struct {
				Layers []struct {
					Name       string `xml:"Name"`
					Dimensions []struct {
						Name    string `xml:"name,attr"`
						Default string `xml:"default,attr"`
					} `xml:"Dimension"`
				} `xml:"Layer"`
			} `xml:"Layer"`
		} `xml:"Capability"`
	}
	if err := xml.Unmarshal(body, &capabilities); err != nil {
		return time.Time{}, fmt.Errorf("read KNMI radar capabilities: %w", err)
	}
	for _, layer := range capabilities.Capability.Layer.Layers {
		if layer.Name != layerName {
			continue
		}
		for _, dimension := range layer.Dimensions {
			if dimension.Name == dimensionName {
				observed, err := time.Parse(time.RFC3339, dimension.Default)
				if err != nil {
					return time.Time{}, fmt.Errorf("read KNMI %s: %w", dimensionName, err)
				}
				return observed, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("KNMI %s has no %s timestamp", layerName, dimensionName)
}

func getLocalRadar(ctx context.Context, lat, lon float64) (*localRadarImage, error) {
	return getLocalRadarRadius(ctx, lat, lon, int(localRadarRadiusM/1000))
}

func getLocalRadarRadius(ctx context.Context, lat, lon float64, radiusKM int) (*localRadarImage, error) {
	// The composite's published geographic coverage; do not query unsupported locations.
	if lat < 48.895305 || lat > 55.973601 || lon < 0 || lon > 10.856454 {
		return nil, nil
	}
	observed, err := latestRadarTime(ctx)
	if err != nil {
		return nil, err
	}
	if !freshRadarObservation(observed, time.Now()) {
		return nil, nil
	}
	centerLat, centerLon := math.Round(lat*100)/100, math.Round(lon*100)/100
	key := fmt.Sprintf("%.2f,%.2f,%d,%s", centerLat, centerLon, radiusKM, observed.Format(time.RFC3339))
	return memo(localRadarCache, key, func() (*localRadarImage, error) {
		x, y := radarMercator(centerLat, centerLon)
		radar := &localRadarImage{observed: observed, x: x, y: y, halfWidth: float64(radiusKM) * 1000 / math.Cos(centerLat*math.Pi/180), radiusKM: radiusKM}
		bbox := localRadarSourceBBox(radar)
		source, err := memo(localRadarSourceCache, bbox+":"+observed.Format(time.RFC3339), func() (image.Image, error) {
			body, err := radarWMSRequest(ctx, url.Values{"REQUEST": {"GetMap"}, "VERSION": {"1.3.0"}, "LAYERS": {"precipitation"}, "STYLES": {"radar/nearest"}, "FORMAT": {"image/png"}, "TRANSPARENT": {"TRUE"}, "CRS": {"EPSG:3857"}, "BBOX": {bbox}, "WIDTH": {strconv.Itoa(localRadarSourceSize)}, "HEIGHT": {strconv.Itoa(localRadarSourceSize)}, "TIME": {observed.Format(time.RFC3339)}})
			if err != nil {
				return nil, err
			}
			overlay, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				return nil, fmt.Errorf("decode KNMI radar PNG: %w", err)
			}
			return recolorLocalRadarRain(overlay), nil
		})
		if err != nil {
			return nil, err
		}
		radar.overlay = cropLocalRadarOverlay(source, radiusKM)
		if radar.shown() {
			mapCtx, cancel := context.WithTimeout(ctx, radarBasemapTimeout)
			defer cancel()
			var wg sync.WaitGroup
			wg.Go(func() { radar.background = localRadarBasemap(mapCtx, radar, "grijs") })
			wg.Go(func() { radar.labels = localRadarBasemap(mapCtx, radar, "labels") })
			wg.Wait()
		}
		return radar, nil
	})
}

func freshRadarObservation(observed, now time.Time) bool {
	age := now.Sub(observed)
	return age >= -2*time.Minute && age <= localRadarMaxAge
}

func hasLocalRadarRain(overlay image.Image) bool {
	center := float64(localRadarSize) / 2
	for y := range localRadarSize {
		for x := range localRadarSize {
			if math.Hypot(float64(x)+.5-center, float64(y)+.5-center) > center {
				continue
			}
			_, _, _, a := overlay.At(x, y).RGBA()
			if a >= 0x8000 {
				return true
			}
		}
	}
	return false
}

func renderLocalRadar(radar *localRadarImage, lat, lon float64) ([]byte, error) {
	return renderLocalRadarTheme(radar, lat, lon, false)
}

func renderLocalRadarTheme(radar *localRadarImage, lat, lon float64, dark bool) ([]byte, error) {
	canvas, err := renderLocalRadarImageTheme(radar, lat, lon, dark)
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, fmt.Errorf("encode local radar: %w", err)
	}
	return encoded.Bytes(), nil
}

func renderLocalRadarImageTheme(radar *localRadarImage, lat, lon float64, dark bool) (*image.RGBA, error) {
	radarLandOnce.Do(func() {
		body, err := radarLandFS.ReadFile("radar/land.json")
		if err != nil {
			radarLandErr = fmt.Errorf("read radar land geometry: %w", err)
			return
		}
		if err := json.Unmarshal(body, &radarLand); err != nil {
			radarLandErr = fmt.Errorf("decode radar land geometry: %w", err)
		}
	})
	if radarLandErr != nil {
		return nil, radarLandErr
	}
	canvas := image.NewRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
	water := color.RGBA{186, 209, 216, 255}
	land := color.RGBA{184, 194, 168, 255}
	background, labels := radar.background, radar.labels
	if dark {
		water, land = color.RGBAModel.Convert(darkRadarWater).(color.RGBA), color.RGBAModel.Convert(darkRadarLand).(color.RGBA)
		background, labels = darkRadarLayer(background, false), darkRadarLayer(labels, true)
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(water), image.Point{}, draw.Src)
	project := func(lat, lon float64) (float64, float64) {
		x, y := radarMercator(lat, lon)
		return (x - radar.x + radar.halfWidth) * localRadarSize / (2 * radar.halfWidth), (radar.y + radar.halfWidth - y) * localRadarSize / (2 * radar.halfWidth)
	}
	opaque, ok := background.(interface{ Opaque() bool })
	if !ok || !opaque.Opaque() {
		paths := make([][][2]float64, len(radarLand))
		for i, ring := range radarLand {
			for _, point := range ring {
				x, y := project(point[1], point[0])
				paths[i] = append(paths[i], [2]float64{x, y})
			}
		}
		for y := range localRadarSize {
			var crossings []float64
			scan := float64(y) + .5
			for _, path := range paths {
				for i, b := range path {
					a := path[(i+len(path)-1)%len(path)]
					if (a[1] <= scan && b[1] > scan) || (b[1] <= scan && a[1] > scan) {
						crossings = append(crossings, a[0]+(scan-a[1])*(b[0]-a[0])/(b[1]-a[1]))
					}
				}
			}
			slices.Sort(crossings)
			for i := 0; i+1 < len(crossings); i += 2 {
				for x := max(0, int(math.Ceil(crossings[i]-.5))); x < min(localRadarSize, int(math.Ceil(crossings[i+1]-.5))); x++ {
					canvas.SetRGBA(x, y, land)
				}
			}
		}
	}
	if background != nil {
		draw.Draw(canvas, canvas.Bounds(), background, image.Point{}, draw.Over)
	}
	draw.Draw(canvas, canvas.Bounds(), radar.overlay, image.Point{}, draw.Over)
	if labels != nil {
		draw.Draw(canvas, canvas.Bounds(), labels, image.Point{}, draw.Over)
	}
	centerX, centerY := project(lat, lon)
	for y := range localRadarSize {
		for x := range localRadarSize {
			d := math.Hypot(float64(x)-centerX, float64(y)-centerY)
			if d <= 5 {
				canvas.SetRGBA(x, y, color.RGBA{6, 90, 130, 255})
			} else if d <= 8 {
				canvas.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	return canvas, nil
}

func handleLocalRadar(w http.ResponseWriter, r *http.Request) {
	dark, err := darkRadarRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	loc, _, err := resolveRequestLocation(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	radiusKM, err := localRadarRadiusRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	radar, err := getLocalRadarRadius(r.Context(), loc.Latitude, loc.Longitude, radiusKM)
	if err != nil {
		slog.Debug("local radar unavailable", "err", err)
		http.Error(w, "local radar unavailable", http.StatusBadGateway)
		return
	}
	if !radar.shown() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := renderLocalRadarTheme(radar, loc.Latitude, loc.Longitude, dark)
	if err != nil {
		slog.Warn("render local radar", "err", err)
		http.Error(w, "local radar unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Radar-Observed-At", radar.observed.Format(time.RFC3339))
	if _, err := w.Write(body); err != nil {
		slog.Log(r.Context(), LevelTrace, "write local radar", "err", err)
	}
}
