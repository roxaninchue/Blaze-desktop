package cloudai

// Weather from Blaze Cloud (gateway 0.6.8, GET /v1/weather): the one source for weather in chat
// (get_weather) and on the Home card, so both always agree. Open-Meteo with MET Norway as a
// fallback, day/night aware; a 503 weather_unavailable means both failed, and Blaze must say so
// instead of guessing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Weather is the gateway's answer.
type Weather struct {
	Location struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"location"`
	Units       string   `json:"units"`
	Temperature float64  `json:"temperature"`
	FeelsLike   *float64 `json:"feels_like"`
	TempUnit    string   `json:"temperature_unit"`
	Condition   string   `json:"condition"`
	IsDay       bool     `json:"is_day"`
	Humidity    *float64 `json:"humidity_pct"`
	WindSpeed   *float64 `json:"wind_speed"`
	WindUnit    string   `json:"wind_unit"`
	ObservedAt  string   `json:"observed_at"`
	Source      string   `json:"source"`
	Summary     string   `json:"summary"`
}

// ErrWeatherUnavailable: neither weather source answered (or Blaze Cloud can't be reached).
var ErrWeatherUnavailable = errors.New("weather_unavailable")

// ErrSignedOut: weather needs a Blaze account.
var ErrSignedOut = errors.New("signed_out")

// WeatherURL is the request for lat/lon (rounded to 0.01°, the gateway's cache cell) and units
// ("metric", "imperial" or "" for the gateway's default).
func WeatherURL(base string, lat, lon float64, units string) string {
	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(round2(lat), 'f', 2, 64))
	q.Set("lon", strconv.FormatFloat(round2(lon), 'f', 2, 64))
	if units == "metric" || units == "imperial" {
		q.Set("units", units)
	}
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/v1/weather?" + q.Encode()
}

func round2(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v*100+0.5)) / 100
	}
	return float64(int64(v*100+0.5)) / 100
}

// Weather asks Blaze Cloud for the current weather at lat/lon.
func (g *Gateway) Weather(ctx context.Context, lat, lon float64, units string) (*Weather, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, fmt.Errorf("lat/lon out of range")
	}
	tok := ""
	if g.Token != nil {
		tok = g.Token()
	}
	if tok == "" {
		return nil, ErrSignedOut
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", WeatherURL(g.Base, lat, lon, units), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Blaze-Desktop")
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, ErrWeatherUnavailable
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == 401:
		return nil, ErrSignedOut
	case resp.StatusCode != 200:
		return nil, ErrWeatherUnavailable
	}
	var w Weather
	if json.Unmarshal(raw, &w) != nil || w.Source == "" || w.TempUnit == "" {
		return nil, ErrWeatherUnavailable
	}
	return &w, nil
}
