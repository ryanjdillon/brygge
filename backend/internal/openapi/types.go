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

// Wrapper response types matching handler map[string]any envelopes.

type AvailabilityResponse struct {
	Dates []DayAvailability `json:"dates" doc:"Availability per date"`
}

type PricingResponse struct {
	Items []PriceItem `json:"items" doc:"Pricing items"`
}

type ProductsResponse struct {
	Products []Product `json:"products" doc:"Product list"`
}

type DocumentsResponse struct {
	Documents []Document `json:"documents" doc:"Document list"`
}

type ConsentsResponse struct {
	Consents []Consent `json:"consents" doc:"User consents"`
}

type DeletionRequestsResponse struct {
	Requests []DeletionRequest `json:"requests" doc:"Deletion requests"`
}

type LegalDocumentsResponse struct {
	Documents []LegalDocument `json:"documents" doc:"Legal documents"`
}

type NotificationPreferencesResponse struct {
	Categories []NotificationPreference `json:"categories" doc:"Notification preference categories"`
}

type NotificationConfigResponse struct {
	Categories []NotificationConfig `json:"categories" doc:"Notification config categories"`
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
	ID           string    `json:"id" doc:"Resource UUID"`
	ClubID       string    `json:"club_id" doc:"Club UUID"`
	Type         string    `json:"type" doc:"Resource type key"`
	Name         string    `json:"name" doc:"Resource name"`
	Description  string    `json:"description" doc:"Resource description"`
	Capacity     int       `json:"capacity" doc:"Total capacity"`
	PricePerUnit float64   `json:"price_per_unit" doc:"Price per booking unit"`
	Unit         string    `json:"unit" doc:"Booking unit (e.g. night, hour)"`
	CreatedAt    time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt    time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type Booking struct {
	ID             string     `json:"id" doc:"Booking UUID"`
	ResourceID     string     `json:"resource_id" doc:"Resource UUID"`
	ResourceUnitID *string    `json:"resource_unit_id,omitempty" doc:"Resource unit UUID"`
	UserID         *string    `json:"user_id,omitempty" doc:"Booking member UUID"`
	ClubID         string     `json:"club_id" doc:"Club UUID"`
	StartDate      time.Time  `json:"start_date" doc:"Start date (ISO 8601)"`
	EndDate        time.Time  `json:"end_date" doc:"End date (ISO 8601)"`
	Status         string     `json:"status" enum:"pending,confirmed,cancelled,completed,no_show" doc:"Booking status"`
	GuestName      *string    `json:"guest_name,omitempty" doc:"Guest name"`
	GuestEmail     *string    `json:"guest_email,omitempty" doc:"Guest email"`
	GuestPhone     *string    `json:"guest_phone,omitempty" doc:"Guest phone"`
	PaymentID      *string    `json:"payment_id,omitempty" doc:"Payment UUID"`
	BoatLengthM    *float64   `json:"boat_length_m,omitempty" doc:"Boat length in metres"`
	BoatBeamM      *float64   `json:"boat_beam_m,omitempty" doc:"Boat beam in metres"`
	BoatDraftM     *float64   `json:"boat_draft_m,omitempty" doc:"Boat draft in metres"`
	Notes          string     `json:"notes" doc:"Optional notes"`
	CreatedAt      time.Time  `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt      time.Time  `json:"updated_at" doc:"Last update timestamp"`
}

type BookingAdmin struct {
	Booking
	ResourceName string  `json:"resource_name" doc:"Resource display name"`
	ResourceType string  `json:"resource_type" doc:"Resource type"`
	UserName     *string `json:"user_name,omitempty" doc:"Booking member name"`
	UserEmail    *string `json:"user_email,omitempty" doc:"Booking member email"`
}

type CreateBookingRequest struct {
	ResourceType string   `json:"resource_type" doc:"Resource type to book"`
	StartDate    string   `json:"start_date" doc:"Start date (ISO 8601)"`
	EndDate      string   `json:"end_date" doc:"End date (ISO 8601)"`
	BoatLengthM  *float64 `json:"boat_length_m,omitempty" doc:"Boat length in metres"`
	BoatBeamM    *float64 `json:"boat_beam_m,omitempty" doc:"Boat beam in metres"`
	BoatDraftM   *float64 `json:"boat_draft_m,omitempty" doc:"Boat draft in metres"`
	Season       string   `json:"season,omitempty" doc:"Season identifier"`
	GuestName    string   `json:"guest_name,omitempty" doc:"Guest name"`
	GuestEmail   string   `json:"guest_email,omitempty" doc:"Guest email"`
	GuestPhone   string   `json:"guest_phone,omitempty" doc:"Guest phone"`
	Notes        string   `json:"notes,omitempty" doc:"Optional notes"`
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
	ID          string    `json:"id" doc:"Event UUID"`
	ClubID      string    `json:"club_id" doc:"Club UUID"`
	Title       string    `json:"title" doc:"Event title"`
	Description string    `json:"description" doc:"Event description"`
	Location    string    `json:"location" doc:"Event location"`
	StartTime   time.Time `json:"start_time" doc:"Start date/time (ISO 8601)"`
	EndTime     time.Time `json:"end_time" doc:"End date/time (ISO 8601)"`
	Tag         string    `json:"tag" doc:"Event tag/category"`
	IsPublic    bool      `json:"is_public" doc:"Whether event is publicly visible"`
	CreatedBy   string    `json:"created_by" doc:"Creator user UUID"`
	CreatedAt   time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type CreateEventRequest struct {
	Title       string `json:"title" minLength:"1" doc:"Event title"`
	Description string `json:"description,omitempty" doc:"Event description"`
	Location    string `json:"location,omitempty" doc:"Event location"`
	StartTime   string `json:"start_time" doc:"Start date/time (ISO 8601)"`
	EndTime     string `json:"end_time" doc:"End date/time (ISO 8601)"`
	Tag         string `json:"tag,omitempty" doc:"Event tag/category"`
	IsPublic    bool   `json:"is_public" doc:"Whether event is publicly visible"`
}

// --- Members ---

type DirectoryMember struct {
	FullName string  `json:"full_name" doc:"Member's full name"`
	Phone    *string `json:"phone,omitempty" doc:"Phone number"`
	Email    *string `json:"email,omitempty" doc:"Email address"`
}

type MemberProfile struct {
	ID          string    `json:"id" doc:"Member UUID"`
	ClubID      string    `json:"club_id" doc:"Club UUID"`
	Email       string    `json:"email" doc:"Email address"`
	FullName    string    `json:"full_name" doc:"Full name"`
	Phone       string    `json:"phone" doc:"Phone number"`
	AddressLine string    `json:"address_line" doc:"Street address"`
	PostalCode  string    `json:"postal_code" doc:"Postal code"`
	City        string    `json:"city" doc:"City"`
	IsLocal     bool      `json:"is_local" doc:"Whether member is a local resident"`
	CreatedAt   time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type MemberSlip struct {
	SlipID   string   `json:"slip_id" doc:"Slip UUID"`
	Number   string   `json:"number" doc:"Slip number"`
	Section  string   `json:"section" doc:"Harbour section"`
	LengthM  *float64 `json:"length_m,omitempty" doc:"Length in metres"`
	WidthM   *float64 `json:"width_m,omitempty" doc:"Width in metres"`
	DepthM   *float64 `json:"depth_m,omitempty" doc:"Depth in metres"`
	Status   string   `json:"status" doc:"Slip status"`
	AssignedAt string `json:"assigned_at" doc:"Assignment date"`
}

type DashboardSlip struct {
	Number   string `json:"number" doc:"Slip number"`
	Location string `json:"location" doc:"Harbour section/location"`
}

type DashboardResponse struct {
	MembershipStatus      string         `json:"membershipStatus" doc:"Current membership status"`
	QueuePosition         *int           `json:"queuePosition" doc:"Position in waiting list"`
	QueueTotal            *int           `json:"queueTotal" doc:"Total entries in waiting list"`
	Slip                  *DashboardSlip `json:"slip,omitempty" doc:"Assigned slip info"`
	UpcomingBookingsCount int            `json:"upcomingBookingsCount" doc:"Number of upcoming bookings"`
}

// --- Boats ---

type BoatModel struct {
	ID           string    `json:"id" doc:"Model UUID"`
	Manufacturer string    `json:"manufacturer" doc:"Boat manufacturer"`
	Model        string    `json:"model" doc:"Boat model name"`
	YearFrom     *int      `json:"year_from,omitempty" doc:"Production start year"`
	YearTo       *int      `json:"year_to,omitempty" doc:"Production end year"`
	LengthM      *float64  `json:"length_m,omitempty" doc:"Length in metres"`
	BeamM        *float64  `json:"beam_m,omitempty" doc:"Beam in metres"`
	DraftM       *float64  `json:"draft_m,omitempty" doc:"Draft in metres"`
	WeightKg     *float64  `json:"weight_kg,omitempty" doc:"Weight in kilograms"`
	BoatType     string    `json:"boat_type" doc:"Type classification"`
	Source       string    `json:"source" doc:"Data source"`
	CreatedAt    time.Time `json:"created_at" doc:"Creation timestamp"`
}

type Boat struct {
	ID                    string     `json:"id" doc:"Boat UUID"`
	UserID                string     `json:"user_id" doc:"Owner user UUID"`
	ClubID                string     `json:"club_id" doc:"Club UUID"`
	Name                  string     `json:"name" doc:"Boat name"`
	Type                  string     `json:"type" doc:"Boat type"`
	Manufacturer          string     `json:"manufacturer" doc:"Manufacturer"`
	Model                 string     `json:"model" doc:"Model name"`
	LengthM               *float64   `json:"length_m,omitempty" doc:"Length in metres"`
	BeamM                 *float64   `json:"beam_m,omitempty" doc:"Beam in metres"`
	DraftM                *float64   `json:"draft_m,omitempty" doc:"Draft in metres"`
	WeightKg              *float64   `json:"weight_kg,omitempty" doc:"Weight in kilograms"`
	RegistrationNumber    string     `json:"registration_number" doc:"Registration number"`
	BoatModelID           *string    `json:"boat_model_id,omitempty" doc:"Linked boat model UUID"`
	MeasurementsConfirmed bool       `json:"measurements_confirmed" doc:"Whether measurements are confirmed"`
	ConfirmedBy           *string    `json:"confirmed_by,omitempty" doc:"UUID of confirming user"`
	ConfirmedAt           *time.Time `json:"confirmed_at,omitempty" doc:"Confirmation timestamp"`
	CreatedAt             time.Time  `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt             time.Time  `json:"updated_at" doc:"Last update timestamp"`
}

type UnconfirmedBoat struct {
	ID                 string   `json:"id" doc:"Boat UUID"`
	UserID             string   `json:"user_id" doc:"Owner user UUID"`
	OwnerName          string   `json:"owner_name" doc:"Owner name"`
	Name               string   `json:"name" doc:"Boat name"`
	Type               string   `json:"type" doc:"Boat type"`
	Manufacturer       string   `json:"manufacturer" doc:"Manufacturer"`
	Model              string   `json:"model" doc:"Model name"`
	LengthM            *float64 `json:"length_m,omitempty" doc:"Length in metres"`
	BeamM              *float64 `json:"beam_m,omitempty" doc:"Beam in metres"`
	DraftM             *float64 `json:"draft_m,omitempty" doc:"Draft in metres"`
	WeightKg           *float64 `json:"weight_kg,omitempty" doc:"Weight in kilograms"`
	RegistrationNumber string   `json:"registration_number" doc:"Registration number"`
	BoatModelID        *string  `json:"boat_model_id,omitempty" doc:"Linked boat model UUID"`
	CreatedAt          string   `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt          string   `json:"updated_at" doc:"Last update timestamp"`
}

// --- Slips ---

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

// --- Slip Shares ---

type SlipShare struct {
	ID               string    `json:"id" doc:"Share UUID"`
	SlipAssignmentID string    `json:"slip_assignment_id" doc:"Slip assignment UUID"`
	ClubID           string    `json:"club_id" doc:"Club UUID"`
	AvailableFrom    string    `json:"available_from" doc:"Start date"`
	AvailableTo      string    `json:"available_to" doc:"End date"`
	Notes            string    `json:"notes" doc:"Notes"`
	Status           string    `json:"status" doc:"Share status"`
	CreatedAt        time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt        time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type SlipShareAdmin struct {
	SlipShare
	SlipNumber string `json:"slip_number" doc:"Slip number"`
	Section    string `json:"section" doc:"Harbour section"`
	MemberName string `json:"member_name" doc:"Member name"`
}

type SlipShareRebate struct {
	ID           string    `json:"id" doc:"Rebate UUID"`
	SlipShareID  string    `json:"slip_share_id" doc:"Share UUID"`
	BookingID    string    `json:"booking_id" doc:"Booking UUID"`
	NightsRented int       `json:"nights_rented" doc:"Number of nights rented"`
	RebatePct    float64   `json:"rebate_pct" doc:"Rebate percentage"`
	RentalIncome float64   `json:"rental_income" doc:"Total rental income"`
	RebateAmount float64   `json:"rebate_amount" doc:"Calculated rebate amount"`
	Status       string    `json:"status" doc:"Rebate status"`
	CreatedAt    time.Time `json:"created_at" doc:"Creation timestamp"`
}

// --- Waiting List ---

type WaitingListEntry struct {
	ID            string     `json:"id" doc:"Entry UUID"`
	UserID        string     `json:"user_id" doc:"User UUID"`
	ClubID        string     `json:"club_id" doc:"Club UUID"`
	Position      int        `json:"position" doc:"Queue position"`
	IsLocal       bool       `json:"is_local" doc:"Whether member is local resident"`
	Status        string     `json:"status" doc:"Entry status"`
	OfferDeadline *time.Time `json:"offer_deadline,omitempty" doc:"Deadline to accept offer"`
	CreatedAt     time.Time  `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt     time.Time  `json:"updated_at" doc:"Last update timestamp"`
}

type WaitingListEntryAdmin struct {
	WaitingListEntry
	FullName      string   `json:"full_name" doc:"Member full name"`
	Email         string   `json:"email" doc:"Member email"`
	Phone         string   `json:"phone" doc:"Member phone"`
	BoatID        *string  `json:"boat_id,omitempty" doc:"Boat UUID"`
	BoatName      *string  `json:"boat_name,omitempty" doc:"Boat name"`
	BoatBeam      *float64 `json:"boat_beam,omitempty" doc:"Boat beam in metres"`
	BoatConfirmed *bool    `json:"boat_confirmed,omitempty" doc:"Whether boat measurements are confirmed"`
}

type PortalWaitingListEntry struct {
	Position int      `json:"position" doc:"Queue position"`
	IsLocal  bool     `json:"is_local" doc:"Whether member is local resident"`
	IsYou    bool     `json:"is_you" doc:"Whether this is the current user"`
	Name     string   `json:"name" doc:"Member name"`
	Status   string   `json:"status" doc:"Entry status"`
	BoatName *string  `json:"boat_name,omitempty" doc:"Boat name"`
	BoatBeam *float64 `json:"boat_beam,omitempty" doc:"Boat beam in metres"`
}

// --- Notifications ---

type NotificationPreference struct {
	Category string `json:"category" doc:"Notification category"`
	Enabled  bool   `json:"enabled" doc:"Whether enabled"`
	Required bool   `json:"required" doc:"Whether category is required (cannot disable)"`
	Default  bool   `json:"default" doc:"Default enabled state"`
}

type NotificationConfig struct {
	Category string `json:"category" doc:"Notification category"`
	Required bool   `json:"required" doc:"Whether category is required"`
	LeadDays *int   `json:"lead_days,omitempty" doc:"Days before event to notify"`
}

// --- Projects ---

type Project struct {
	ID          string    `json:"id" doc:"Project UUID"`
	ClubID      string    `json:"club_id" doc:"Club UUID"`
	Name        string    `json:"name" doc:"Project name"`
	Description string    `json:"description" doc:"Project description"`
	CreatedBy   string    `json:"created_by" doc:"Creator user UUID"`
	CreatedAt   time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   time.Time `json:"updated_at" doc:"Last update timestamp"`
}

type ProjectWithCounts struct {
	Project
	TodoCount       int `json:"todo_count" doc:"Number of todo tasks"`
	InProgressCount int `json:"in_progress_count" doc:"Number of in-progress tasks"`
	DoneCount       int `json:"done_count" doc:"Number of completed tasks"`
}

type MaterialItem struct {
	Item     string   `json:"item" doc:"Material name"`
	Quantity *float64 `json:"quantity,omitempty" doc:"Quantity needed"`
	Unit     string   `json:"unit,omitempty" doc:"Unit of measurement"`
	EstCost  *float64 `json:"est_cost,omitempty" doc:"Estimated cost"`
}

type Task struct {
	ID               string        `json:"id" doc:"Task UUID"`
	ProjectID        string        `json:"project_id" doc:"Project UUID"`
	ClubID           string        `json:"club_id" doc:"Club UUID"`
	Title            string        `json:"title" doc:"Task title"`
	Description      string        `json:"description" doc:"Task description"`
	AssigneeID       *string       `json:"assignee_id" doc:"Assignee user UUID"`
	Status           string        `json:"status" doc:"Task status"`
	Priority         string        `json:"priority" doc:"Task priority"`
	DueDate          *string       `json:"due_date,omitempty" doc:"Due date"`
	CreatedBy        string        `json:"created_by" doc:"Creator user UUID"`
	CreatedAt        time.Time     `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt        time.Time     `json:"updated_at" doc:"Last update timestamp"`
	EstimatedHours   *float64      `json:"estimated_hours" doc:"Estimated hours"`
	ActualHours      *float64      `json:"actual_hours" doc:"Actual hours spent"`
	AnsvarligID      *string       `json:"ansvarlig_id" doc:"Responsible person UUID"`
	MaxCollaborators int           `json:"max_collaborators" doc:"Maximum collaborators"`
	Materials        []MaterialItem `json:"materials" doc:"Required materials"`
	ParticipantCount int           `json:"participant_count" doc:"Number of participants"`
}

type GroupedTasks struct {
	Todo       []Task `json:"todo" doc:"Todo tasks"`
	InProgress []Task `json:"in_progress" doc:"In-progress tasks"`
	Done       []Task `json:"done" doc:"Completed tasks"`
}

// --- Products / Commerce ---

type ProductVariant struct {
	ID            string   `json:"id" doc:"Variant UUID"`
	Size          string   `json:"size" doc:"Size"`
	Color         string   `json:"color" doc:"Color"`
	Stock         int      `json:"stock" doc:"Available stock"`
	PriceOverride *float64 `json:"price_override,omitempty" doc:"Price override"`
	ImageURL      string   `json:"image_url" doc:"Variant image URL"`
	SortOrder     int      `json:"sort_order" doc:"Display order"`
}

type Product struct {
	ID          string           `json:"id" doc:"Product UUID"`
	Name        string           `json:"name" doc:"Product name"`
	Description string           `json:"description" doc:"Product description"`
	Price       float64          `json:"price" doc:"Base price"`
	Currency    string           `json:"currency" doc:"Currency code"`
	ImageURL    string           `json:"image_url" doc:"Product image URL"`
	Stock       int              `json:"stock" doc:"Available stock"`
	IsActive    bool             `json:"is_active" doc:"Whether product is active"`
	SortOrder   int              `json:"sort_order" doc:"Display order"`
	Variants    []ProductVariant `json:"variants" doc:"Product variants"`
	CreatedAt   time.Time        `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   time.Time        `json:"updated_at" doc:"Last update timestamp"`
}

type PriceItem struct {
	ID                  string  `json:"id" doc:"Price item UUID"`
	Category            string  `json:"category" doc:"Price category"`
	Name                string  `json:"name" doc:"Item name"`
	Description         string  `json:"description" doc:"Item description"`
	Amount              float64 `json:"amount" doc:"Price amount"`
	Currency            string  `json:"currency" doc:"Currency code"`
	Unit                string  `json:"unit" doc:"Pricing unit"`
	InstallmentsAllowed bool    `json:"installments_allowed" doc:"Whether installments are allowed"`
	MaxInstallments     int     `json:"max_installments" doc:"Maximum installment count"`
	Metadata            any     `json:"metadata" doc:"Additional metadata"`
	SortOrder           int     `json:"sort_order" doc:"Display order"`
	IsActive            bool    `json:"is_active" doc:"Whether item is active"`
}

// --- Feature Requests ---

type FeatureRequest struct {
	ID          string    `json:"id" doc:"Feature request UUID"`
	ClubID      string    `json:"club_id" doc:"Club UUID"`
	Title       string    `json:"title" doc:"Request title"`
	Description string    `json:"description" doc:"Request description"`
	Status      string    `json:"status" doc:"Request status"`
	SubmittedBy string    `json:"submitted_by" doc:"Submitter user UUID"`
	VoteCount   int       `json:"vote_count" doc:"Total vote count"`
	UserVote    *int      `json:"user_vote" doc:"Current user's vote"`
	CreatedAt   time.Time `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   time.Time `json:"updated_at" doc:"Last update timestamp"`
}

// --- Documents ---

type Document struct {
	ID          string `json:"id" doc:"Document UUID"`
	Title       string `json:"title" doc:"Document title"`
	Filename    string `json:"filename" doc:"Original filename"`
	ContentType string `json:"content_type" doc:"MIME content type"`
	SizeBytes   int64  `json:"size_bytes" doc:"File size in bytes"`
	Visibility  string `json:"visibility" doc:"Visibility level"`
	CreatedAt   string `json:"created_at" doc:"Creation timestamp"`
	UpdatedAt   string `json:"updated_at" doc:"Last update timestamp"`
	UploadedBy  string `json:"uploaded_by" doc:"Uploader user UUID"`
}

// --- Financials ---

type FinancialSummary struct {
	TotalDuesReceived   float64 `json:"total_dues_received" doc:"Total dues received"`
	TotalOutstanding    float64 `json:"total_outstanding" doc:"Total outstanding amount"`
	TotalOverdue        float64 `json:"total_overdue" doc:"Total overdue amount"`
	TotalAndelCollected float64 `json:"total_andel_collected" doc:"Total andel collected"`
	TotalBookingRevenue float64 `json:"total_booking_revenue" doc:"Total booking revenue"`
	Year                *int    `json:"year,omitempty" doc:"Financial year"`
}

type Payment struct {
	ID             string     `json:"id" doc:"Payment UUID"`
	UserID         string     `json:"user_id" doc:"User UUID"`
	UserName       string     `json:"user_name" doc:"User name"`
	UserEmail      string     `json:"user_email" doc:"User email"`
	Type           string     `json:"type" doc:"Payment type"`
	Amount         float64    `json:"amount" doc:"Payment amount"`
	Currency       string     `json:"currency" doc:"Currency code"`
	Status         string     `json:"status" doc:"Payment status"`
	Description    string     `json:"description" doc:"Payment description"`
	DueDate        *time.Time `json:"due_date,omitempty" doc:"Due date"`
	PaidAt         *time.Time `json:"paid_at,omitempty" doc:"Payment date"`
	VippsReference string     `json:"vipps_reference" doc:"Vipps payment reference"`
	CreatedAt      time.Time  `json:"created_at" doc:"Creation timestamp"`
}

type PaymentsListResponse struct {
	Payments []Payment `json:"payments" doc:"List of payments"`
	Total    int       `json:"total" doc:"Total count"`
	Page     int       `json:"page" doc:"Current page"`
	PerPage  int       `json:"per_page" doc:"Items per page"`
}

type OverduePayment struct {
	ID          string  `json:"id" doc:"Payment UUID"`
	UserID      string  `json:"user_id" doc:"User UUID"`
	UserName    string  `json:"user_name" doc:"User name"`
	UserEmail   string  `json:"user_email" doc:"User email"`
	UserPhone   string  `json:"user_phone" doc:"User phone"`
	Type        string  `json:"type" doc:"Payment type"`
	Amount      float64 `json:"amount" doc:"Payment amount"`
	Currency    string  `json:"currency" doc:"Currency code"`
	Description string  `json:"description" doc:"Payment description"`
	DueDate     string  `json:"due_date" doc:"Due date"`
	DaysOverdue int     `json:"days_overdue" doc:"Number of days overdue"`
}

type CreateInvoiceRequest struct {
	UserID      string  `json:"user_id" doc:"User UUID"`
	Type        string  `json:"type" doc:"Invoice type"`
	Amount      float64 `json:"amount" doc:"Invoice amount"`
	Description string  `json:"description" doc:"Invoice description"`
	DueDate     string  `json:"due_date" doc:"Due date"`
}

// --- Dugnad ---

type TaskParticipant struct {
	TaskID   string    `json:"task_id" doc:"Task UUID"`
	UserID   string    `json:"user_id" doc:"User UUID"`
	Role     string    `json:"role" doc:"Participant role"`
	Hours    *float64  `json:"hours" doc:"Hours contributed"`
	JoinedAt time.Time `json:"joined_at" doc:"Join timestamp"`
	Name     string    `json:"name" doc:"Participant name"`
}

type DugnadHoursSummary struct {
	UserID         string  `json:"user_id" doc:"User UUID"`
	Name           string  `json:"name" doc:"Member name"`
	SignedUpHours  float64 `json:"signed_up_hours" doc:"Hours signed up for"`
	CompletedHours float64 `json:"completed_hours" doc:"Hours completed"`
	RequiredHours  float64 `json:"required_hours" doc:"Required hours"`
	Remaining      float64 `json:"remaining" doc:"Remaining hours"`
}

// --- GDPR ---

type DeletionRequest struct {
	ID          string     `json:"id" doc:"Request UUID"`
	Status      string     `json:"status" doc:"Request status"`
	RequestedAt time.Time  `json:"requested_at" doc:"Request timestamp"`
	GraceEnd    time.Time  `json:"grace_end" doc:"Grace period end"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty" doc:"Cancellation timestamp"`
	ProcessedAt *time.Time `json:"processed_at,omitempty" doc:"Processing timestamp"`
}

type Consent struct {
	ID          string    `json:"id" doc:"Consent UUID"`
	ConsentType string    `json:"consent_type" doc:"Type of consent"`
	Version     string    `json:"version" doc:"Consent version"`
	GrantedAt   time.Time `json:"granted_at" doc:"Grant timestamp"`
}

type LegalDocument struct {
	ID          string    `json:"id" doc:"Document UUID"`
	DocType     string    `json:"doc_type" doc:"Document type"`
	Version     string    `json:"version" doc:"Document version"`
	Content     string    `json:"content" doc:"Document content"`
	PublishedAt time.Time `json:"published_at" doc:"Publication timestamp"`
}

// --- Admin Users ---

type AdminUser struct {
	ID        string    `json:"id" doc:"User UUID"`
	FullName  string    `json:"full_name" doc:"Full name"`
	Email     string    `json:"email" doc:"Email address"`
	Phone     string    `json:"phone" doc:"Phone number"`
	Roles     []string  `json:"roles" doc:"Assigned roles"`
	CreatedAt time.Time `json:"created_at" doc:"Creation timestamp"`
}

type AdminUsersResponse struct {
	Users      []AdminUser `json:"users" doc:"List of users"`
	TotalCount int         `json:"total_count" doc:"Total user count"`
}

// --- Broadcasts ---

type Broadcast struct {
	ID         string    `json:"id" doc:"Broadcast UUID"`
	Subject    string    `json:"subject" doc:"Email subject"`
	Body       string    `json:"body" doc:"Email body"`
	Recipients string    `json:"recipients" doc:"Recipient group (all, members, styre, slip_owners)"`
	SentBy     string    `json:"sent_by" doc:"Sender user UUID"`
	SentAt     time.Time `json:"sent_at" doc:"Send timestamp"`
	CreatedAt  time.Time `json:"created_at" doc:"Creation timestamp"`
}
