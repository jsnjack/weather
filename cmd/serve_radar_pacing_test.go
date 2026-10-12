package cmd

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRadarPublishedRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name, key, host string
		minimum         time.Duration
	}{
		{"anonymous", "", "anonymous.api.dataplatform.knmi.nl", 900 * time.Millisecond},
		{"authenticated", "test-key", "api.dataplatform.knmi.nl", 40 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRadarBasemap(t)
			t.Setenv("KNMI_WMS_API_KEY", tc.key)
			radarWMSGate.Lock()
			oldNext, oldCooldown := radarWMSGate.next, radarWMSGate.cooldown
			radarWMSGate.next = time.Time{}
			radarWMSGate.cooldown = time.Time{}
			radarWMSGate.Unlock()
			t.Cleanup(func() {
				radarWMSGate.Lock()
				radarWMSGate.next, radarWMSGate.cooldown = oldNext, oldCooldown
				radarWMSGate.Unlock()
			})
			var body bytes.Buffer
			if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 256, 256))); err != nil {
				t.Fatal(err)
			}
			var calls []time.Time
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, time.Now())
				if r.URL.Host != tc.host || r.Header.Get("Authorization") != tc.key {
					t.Error("request used the wrong endpoint or authentication")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body.Bytes()))}, nil
			})
			for _, at := range []string{"2026-10-12T10:00:00Z", "2026-10-12T10:05:00Z"} {
				if _, err := radarWMSRequest(context.Background(), url.Values{"REQUEST": {"GetMap"}, "TIME": {at}}); err != nil {
					t.Fatal(err)
				}
			}
			if len(calls) != 2 || calls[1].Sub(calls[0]) < tc.minimum {
				t.Fatal("upstream requests exceeded the published per-second rate")
			}
		})
	}
}
