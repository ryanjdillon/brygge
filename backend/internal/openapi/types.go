package openapi

import "time"

// Shared pagination parameters for list endpoints.
type PaginationParams struct {
	Limit  int `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"Items per page"`
	Offset int `query:"offset" minimum:"0" default:"0" doc:"Number of items to skip"`
}

// PaginatedResponse is the standard envelope for paginated list endpoints.
type PaginatedResponse[T any] struct {
	Items   []T  `json:"items" doc:"List of items"`
	Limit   int  `json:"limit" doc:"Applied limit"`
	Offset  int  `json:"offset" doc:"Applied offset"`
	HasMore bool `json:"has_more" doc:"Whether more items exist beyond this page"`
}

// --- Health ---

type HealthServiceStatus struct {
	Database string `json:"database" example:"ok" doc:"Database connectivity status"`
	Redis    string `json:"redis" example:"ok" doc:"Redis connectivity status"`
}

type HealthPoolStats struct {
	TotalConns    int32 `json:"total_conns" doc:"Total connections in pool"`
	IdleConns     int32 `json:"idle_conns" doc:"Idle connections in pool"`
	AcquiredConns int32 `json:"acquired_conns" doc:"Currently acquired connections"`
	MaxConns      int32 `json:"max_conns" doc:"Maximum configured connections"`
}

type HealthResponse struct {
	Status   string              `json:"status" example:"ok" enum:"ok,degraded" doc:"Overall health status"`
	Services HealthServiceStatus `json:"services" doc:"Per-service status"`
	Pool     *HealthPoolStats    `json:"pool,omitempty" doc:"Database connection pool statistics"`
}

// --- Features ---

type FeaturesResponse struct {
	Bookings       bool `json:"bookings" doc:"Bookings module enabled"`
	Projects       bool `json:"projects" doc:"Projects module enabled"`
	Calendar       bool `json:"calendar" doc:"Calendar module enabled"`
	Commerce       bool `json:"commerce" doc:"Commerce module enabled"`
	Communications bool `json:"communications" doc:"Communications module enabled"`
}

// --- Weather ---

type WeatherResponse struct {
	Temperature   *float64 `json:"temperature" doc:"Air temperature in °C"`
	WindSpeed     *float64 `json:"wind_speed" doc:"Wind speed in m/s"`
	WindDirection *float64 `json:"wind_direction" doc:"Wind direction in degrees"`
	Humidity      *float64 `json:"humidity" doc:"Relative humidity percentage"`
	SymbolCode    string   `json:"symbol_code" example:"cloudy" doc:"Yr.no weather symbol code"`
}

// --- Map ---

type ClubCoordinatesResponse struct {
	Name      string   `json:"name" example:"Bestum Seilforening" doc:"Club name"`
	Latitude  *float64 `json:"latitude" example:"59.9139" doc:"Club latitude"`
	Longitude *float64 `json:"longitude" example:"10.7522" doc:"Club longitude"`
}

type MapMarker struct {
	ID         string    `json:"id" doc:"Marker UUID"`
	ClubID     string    `json:"club_id" doc:"Club UUID"`
	MarkerType string    `json:"marker_type" example:"waypoint" doc:"Marker type"`
	Label      string    `json:"label" example:"Guest dock" doc:"Display label"`
	Lat        float64   `json:"lat" example:"59.9139" doc:"Latitude"`
	Lng        float64   `json:"lng" example:"10.7522" doc:"Longitude"`
	SortOrder  int       `json:"sort_order" doc:"Display order"`
	CreatedAt  time.Time `json:"created_at" doc:"Creation timestamp"`
}

// --- Contact ---

type ContactRequest struct {
	Name    string `json:"name" minLength:"1" doc:"Sender name"`
	Email   string `json:"email" format:"email" doc:"Sender email"`
	Subject string `json:"subject" minLength:"1" doc:"Subject line"`
	Message string `json:"message" minLength:"10" doc:"Message body"`
}

type StatusResponse struct {
	Status string `json:"status" example:"received" doc:"Operation status"`
}

// --- Auth ---

type EmailRegisterRequest struct {
	Email    string `json:"email" format:"email" doc:"Email address"`
	Password string `json:"password" minLength:"8" doc:"Password"`
	Name     string `json:"name" minLength:"1" doc:"Full name"`
	Phone    string `json:"phone,omitempty" doc:"Phone number"`
}

type EmailLoginRequest struct {
	Email    string `json:"email" format:"email" doc:"Email address"`
	Password string `json:"password" doc:"Password"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token" doc:"JWT access token"`
	RefreshToken string `json:"refresh_token" doc:"JWT refresh token"`
	TokenType    string `json:"token_type" example:"Bearer" doc:"Token type"`
	ExpiresIn    int    `json:"expires_in" doc:"Access token TTL in seconds"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" doc:"Refresh token to exchange"`
}

type UserProfile struct {
	ID    string   `json:"id" doc:"User UUID"`
	Email string   `json:"email" doc:"Email address"`
	Name  string   `json:"name" doc:"Full name"`
	Phone string   `json:"phone" doc:"Phone number"`
	Roles []string `json:"roles" doc:"Assigned roles"`
}

// --- Bookings ---

type BookingResource struct {
	ID           string  `json:"id" doc:"Resource UUID"`
	Name         string  `json:"name" doc:"Resource name"`
	ResourceType string  `json:"resource_type" doc:"Resource type key"`
	Capacity     int     `json:"capacity" doc:"Total capacity"`
	PricePerUnit float64 `json:"price_per_unit" doc:"Price per booking unit"`
}

type Booking struct {
	ID           string  `json:"id" doc:"Booking UUID"`
	ResourceID   string  `json:"resource_id" doc:"Resource UUID"`
	ResourceName string  `json:"resource_name" doc:"Resource display name"`
	ResourceType string  `json:"resource_type" doc:"Resource type"`
	UserID       *string `json:"user_id,omitempty" doc:"Booking member UUID"`
	GuestName    *string `json:"guest_name,omitempty" doc:"Guest name"`
	StartDate    string  `json:"start_date" doc:"Start date (ISO 8601)"`
	EndDate      string  `json:"end_date" doc:"End date (ISO 8601)"`
	Status       string  `json:"status" enum:"pending,confirmed,cancelled,completed,no_show" doc:"Booking status"`
	Notes        string  `json:"notes" doc:"Optional notes"`
	CreatedAt    string  `json:"created_at" doc:"Creation timestamp"`
}

type DayAvailability struct {
	Date           string `json:"date" doc:"Date (YYYY-MM-DD)"`
	TotalUnits     int    `json:"total_units" doc:"Total bookable units"`
	AvailableUnits int    `json:"available_units" doc:"Currently available units"`
}

type TodayAvailability struct {
	Available int `json:"available" doc:"Available units today"`
	Total     int `json:"total" doc:"Total units"`
}

type HoistSlot struct {
	Start     string  `json:"start" example:"08:00" doc:"Slot start time"`
	End       string  `json:"end" example:"10:00" doc:"Slot end time"`
	Available bool    `json:"available" doc:"Whether slot is available"`
	BookedBy  *string `json:"booked_by,omitempty" doc:"Name of member who booked"`
}

type HoistSlotsResponse struct {
	Date                string      `json:"date" doc:"Date (YYYY-MM-DD)"`
	SlotDurationMinutes int         `json:"slot_duration_minutes" doc:"Duration per slot"`
	Slots               []HoistSlot `json:"slots" doc:"Available time slots"`
}

// --- Calendar ---

type CalendarEvent struct {
	ID          string  `json:"id" doc:"Event UUID"`
	Title       string  `json:"title" doc:"Event title"`
	Description string  `json:"description" doc:"Event description"`
	StartDate   string  `json:"start_date" doc:"Start date/time (ISO 8601)"`
	EndDate     string  `json:"end_date" doc:"End date/time (ISO 8601)"`
	Location    *string `json:"location,omitempty" doc:"Event location"`
	IsPublic    bool    `json:"is_public" doc:"Whether event is publicly visible"`
}

// --- Members ---

type DirectoryMember struct {
	FullName string  `json:"full_name" doc:"Member's full name"`
	Phone    *string `json:"phone,omitempty" doc:"Phone number"`
	Email    *string `json:"email,omitempty" doc:"Email address"`
}

// --- Slips (admin) ---

type Slip struct {
	ID           string   `json:"id" doc:"Slip UUID"`
	Number       string   `json:"number" doc:"Slip number"`
	Section      string   `json:"section" doc:"Harbour section"`
	LengthM      *float64 `json:"length_m,omitempty" doc:"Length in metres"`
	WidthM       *float64 `json:"width_m,omitempty" doc:"Width in metres"`
	DepthM       *float64 `json:"depth_m,omitempty" doc:"Depth in metres"`
	Status       string   `json:"status" enum:"vacant,occupied,reserved,maintenance" doc:"Slip status"`
	OccupantName *string  `json:"occupant_name,omitempty" doc:"Current occupant name"`
}
