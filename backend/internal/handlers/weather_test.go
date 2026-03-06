package handlers

import (
	"testing"
)

func TestParseYrResponse(t *testing.T) {
	temp := 15.2
	wind := 3.5
	windDir := 180.0
	humidity := 72.0

	tests := []struct {
		name        string
		body        []byte
		wantErr     bool
		checkResult func(t *testing.T, w *weatherResponse)
	}{
		{
			name: "valid response with next_1_hours",
			body: []byte(`{
				"properties": {
					"timeseries": [{
						"data": {
							"instant": {
								"details": {
									"air_temperature": 15.2,
									"wind_speed": 3.5,
									"wind_from_direction": 180.0,
									"relative_humidity": 72.0
								}
							},
							"next_1_hours": {
								"summary": {"symbol_code": "cloudy"}
							}
						}
					}]
				}
			}`),
			checkResult: func(t *testing.T, w *weatherResponse) {
				t.Helper()
				if w.Temperature == nil || *w.Temperature != temp {
					t.Errorf("Temperature = %v, want %v", w.Temperature, temp)
				}
				if w.WindSpeed == nil || *w.WindSpeed != wind {
					t.Errorf("WindSpeed = %v, want %v", w.WindSpeed, wind)
				}
				if w.WindDirection == nil || *w.WindDirection != windDir {
					t.Errorf("WindDirection = %v, want %v", w.WindDirection, windDir)
				}
				if w.Humidity == nil || *w.Humidity != humidity {
					t.Errorf("Humidity = %v, want %v", w.Humidity, humidity)
				}
				if w.SymbolCode != "cloudy" {
					t.Errorf("SymbolCode = %q, want %q", w.SymbolCode, "cloudy")
				}
			},
		},
		{
			name: "falls back to next_6_hours when next_1_hours is absent",
			body: []byte(`{
				"properties": {
					"timeseries": [{
						"data": {
							"instant": {
								"details": {
									"air_temperature": 10.0
								}
							},
							"next_6_hours": {
								"summary": {"symbol_code": "rain"}
							}
						}
					}]
				}
			}`),
			checkResult: func(t *testing.T, w *weatherResponse) {
				t.Helper()
				if w.SymbolCode != "rain" {
					t.Errorf("SymbolCode = %q, want %q", w.SymbolCode, "rain")
				}
			},
		},
		{
			name: "no symbol when neither next_1_hours nor next_6_hours",
			body: []byte(`{
				"properties": {
					"timeseries": [{
						"data": {
							"instant": {
								"details": {
									"air_temperature": 5.0
								}
							}
						}
					}]
				}
			}`),
			checkResult: func(t *testing.T, w *weatherResponse) {
				t.Helper()
				if w.SymbolCode != "" {
					t.Errorf("SymbolCode = %q, want empty", w.SymbolCode)
				}
			},
		},
		{
			name:    "empty timeseries",
			body:    []byte(`{"properties": {"timeseries": []}}`),
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			body:    []byte(`{not valid json`),
			wantErr: true,
		},
		{
			name:    "empty body",
			body:    []byte(``),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseYrResponse(tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkResult != nil {
				tt.checkResult(t, result)
			}
		})
	}
}
