package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
)

const (
	weatherCacheKey = "weather_cache"
	weatherCacheTTL = 10 * time.Minute
	yrUserAgent     = "Brygge/1.0 github.com/brygge-klubb/brygge"
	yrBaseURL       = "https://api.met.no/weatherapi/locationforecast/2.0/compact"
)

type WeatherHandler struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	config *config.Config
	log    zerolog.Logger
	client *http.Client
}

func NewWeatherHandler(
	db *pgxpool.Pool,
	rdb *redis.Client,
	cfg *config.Config,
	log zerolog.Logger,
) *WeatherHandler {
	return &WeatherHandler{
		db:     db,
		redis:  rdb,
		config: cfg,
		log:    log.With().Str("handler", "weather").Logger(),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type weatherResponse struct {
	Temperature   *float64 `json:"temperature"`
	WindSpeed     *float64 `json:"wind_speed"`
	WindDirection *float64 `json:"wind_direction"`
	Humidity      *float64 `json:"humidity"`
	SymbolCode    string   `json:"symbol_code"`
}

func (h *WeatherHandler) HandleGetWeather(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cached, err := h.redis.Get(ctx, weatherCacheKey).Result()
	if err == nil && cached != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(cached))
		return
	}

	lat, lon, err := h.getClubCoordinates(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club coordinates")
		Error(w, http.StatusInternalServerError, "club coordinates not configured")
		return
	}

	url := fmt.Sprintf("%s?lat=%.4f&lon=%.4f", yrBaseURL, lat, lon)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create weather request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("User-Agent", yrUserAgent)

	resp, err := h.client.Do(req)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch weather from Yr.no")
		Error(w, http.StatusBadGateway, "failed to fetch weather data")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.log.Warn().Int("status", resp.StatusCode).Msg("Yr.no returned non-200 status")
		Error(w, http.StatusBadGateway, "weather service returned an error")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to read weather response body")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	weather, err := parseYrResponse(body)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to parse Yr.no response")
		Error(w, http.StatusInternalServerError, "failed to parse weather data")
		return
	}

	responseJSON, err := json.Marshal(weather)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to marshal weather response")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.redis.Set(ctx, weatherCacheKey, string(responseJSON), weatherCacheTTL)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseJSON)
}

func (h *WeatherHandler) getClubCoordinates(ctx context.Context) (float64, float64, error) {
	var lat, lon *float64
	err := h.db.QueryRow(ctx,
		`SELECT latitude, longitude FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&lat, &lon)
	if err == pgx.ErrNoRows {
		return 0, 0, fmt.Errorf("club not found: %s", h.config.ClubSlug)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("querying club: %w", err)
	}
	if lat == nil || lon == nil {
		return 0, 0, fmt.Errorf("club coordinates not set")
	}
	return *lat, *lon, nil
}

func parseYrResponse(body []byte) (*weatherResponse, error) {
	var yr struct {
		Properties struct {
			Timeseries []struct {
				Data struct {
					Instant struct {
						Details struct {
							AirTemperature      *float64 `json:"air_temperature"`
							WindSpeed           *float64 `json:"wind_speed"`
							WindFromDirection   *float64 `json:"wind_from_direction"`
							RelativeHumidity    *float64 `json:"relative_humidity"`
						} `json:"details"`
					} `json:"instant"`
					Next1Hours *struct {
						Summary struct {
							SymbolCode string `json:"symbol_code"`
						} `json:"summary"`
					} `json:"next_1_hours"`
					Next6Hours *struct {
						Summary struct {
							SymbolCode string `json:"symbol_code"`
						} `json:"summary"`
					} `json:"next_6_hours"`
				} `json:"data"`
			} `json:"timeseries"`
		} `json:"properties"`
	}

	if err := json.Unmarshal(body, &yr); err != nil {
		return nil, fmt.Errorf("unmarshaling Yr.no response: %w", err)
	}

	if len(yr.Properties.Timeseries) == 0 {
		return nil, fmt.Errorf("no timeseries data in Yr.no response")
	}

	first := yr.Properties.Timeseries[0].Data
	details := first.Instant.Details

	var symbolCode string
	if first.Next1Hours != nil {
		symbolCode = first.Next1Hours.Summary.SymbolCode
	} else if first.Next6Hours != nil {
		symbolCode = first.Next6Hours.Summary.SymbolCode
	}

	return &weatherResponse{
		Temperature:   details.AirTemperature,
		WindSpeed:     details.WindSpeed,
		WindDirection: details.WindFromDirection,
		Humidity:      details.RelativeHumidity,
		SymbolCode:    symbolCode,
	}, nil
}
