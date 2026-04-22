package cmd

import "math"

const earthRadiusKm = 6371.0

// DestinationPoint returns the lat/lon that is distanceKm away from (lat, lon)
// along the given compass bearing (0° = N, 90° = E). Spherical earth model is
// accurate enough for trip-planning distances (<1000 km).
func DestinationPoint(lat, lon, bearingDeg, distanceKm float64) (float64, float64) {
	phi1 := lat * math.Pi / 180
	lambda1 := lon * math.Pi / 180
	theta := bearingDeg * math.Pi / 180
	delta := distanceKm / earthRadiusKm

	sinPhi1, cosPhi1 := math.Sincos(phi1)
	sinDelta, cosDelta := math.Sincos(delta)
	sinTheta, cosTheta := math.Sincos(theta)

	sinPhi2 := sinPhi1*cosDelta + cosPhi1*sinDelta*cosTheta
	phi2 := math.Asin(sinPhi2)
	y := sinTheta * sinDelta * cosPhi1
	x := cosDelta - sinPhi1*sinPhi2
	lambda2 := lambda1 + math.Atan2(y, x)

	return phi2 * 180 / math.Pi, lambda2 * 180 / math.Pi
}

var compassNames = [8]string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
var compassArrows = [8]string{"↑", "↗", "→", "↘", "↓", "↙", "←", "↖"}

func compassIndex(bearingDeg float64) int {
	idx := int(math.Round(bearingDeg/45)) % 8
	if idx < 0 {
		idx += 8
	}
	return idx
}

// CompassName rounds the bearing to the nearest 8-point compass name.
func CompassName(bearingDeg float64) string {
	return compassNames[compassIndex(bearingDeg)]
}

// CompassArrow rounds the bearing to the nearest 8-point compass arrow glyph.
func CompassArrow(bearingDeg float64) string {
	return compassArrows[compassIndex(bearingDeg)]
}

// HaversineKm returns the great-circle distance in km between two lat/lon points.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180

	sinDPhi := math.Sin(dPhi / 2)
	sinDLambda := math.Sin(dLambda / 2)
	a := sinDPhi*sinDPhi + math.Cos(phi1)*math.Cos(phi2)*sinDLambda*sinDLambda
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}
