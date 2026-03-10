package openapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// stub is a no-op handler used for spec-only registrations.
// These operations document the API contract but actual request
// handling is done by the existing chi handlers.
func stub[I, O any]() func(context.Context, *I) (*O, error) {
	return func(context.Context, *I) (*O, error) { return nil, nil }
}

// RegisterAllOperations registers every API operation for spec generation.
// This is called only in dump-openapi mode on a dedicated router — the
// handlers are stubs since we only need the OpenAPI schema output.
func RegisterAllOperations(api huma.API) {
	registerHealthOps(api)
	registerAuthOps(api)
	registerPublicContentOps(api)
	registerMapOps(api)
	registerBookingOps(api)
	registerCalendarOps(api)
	registerMemberOps(api)
	registerWaitingListOps(api)
	registerSlipShareOps(api)
	registerNotificationOps(api)
	registerForumOps(api)
	registerProjectOps(api)
	registerShoppingListOps(api)
	registerFeatureRequestOps(api)
	registerOrderOps(api)
	registerGDPROps(api)
	registerAdminOps(api)
}

func registerHealthOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/api/v1/health",
		Tags:        []string{"Health"},
		Summary:     "Health check",
		Description: "Returns service health including database and Redis connectivity.",
	}, stub[struct{}, struct{ Body HealthResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-features",
		Method:      http.MethodGet,
		Path:        "/api/v1/features",
		Tags:        []string{"Features"},
		Summary:     "List enabled feature flags",
		Description: "Returns which optional modules are enabled for this deployment.",
	}, stub[struct{}, struct{ Body FeaturesResponse }]())
}

func registerAuthOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "vipps-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/vipps/status",
		Tags:        []string{"Auth"},
		Summary:     "Vipps integration status",
	}, stub[struct{}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "vipps-login",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/vipps/login",
		Tags:        []string{"Auth"},
		Summary:     "Initiate Vipps OAuth login",
		Description: "Redirects to Vipps for authentication.",
	}, stub[struct{}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "vipps-callback",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/vipps/callback",
		Tags:        []string{"Auth"},
		Summary:     "Vipps OAuth callback",
	}, stub[struct {
		Code  string `query:"code"`
		State string `query:"state"`
	}, struct{ Body TokenResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "email-register",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/register",
		Tags:        []string{"Auth"},
		Summary:     "Register with email/password",
	}, stub[struct{ Body EmailRegisterRequest }, struct{ Body TokenResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "email-login",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/login",
		Tags:        []string{"Auth"},
		Summary:     "Login with email/password",
	}, stub[struct{ Body EmailLoginRequest }, struct{ Body TokenResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "refresh-token",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/refresh",
		Tags:        []string{"Auth"},
		Summary:     "Refresh access token",
	}, stub[struct{ Body RefreshRequest }, struct{ Body TokenResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "exchange-auth-code",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/exchange",
		Tags:        []string{"Auth"},
		Summary:     "Exchange authorization code for tokens",
	}, stub[struct {
		Body struct {
			Code string `json:"code" doc:"Authorization code"`
		}
	}, struct{ Body TokenResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/logout",
		Tags:        []string{"Auth"},
		Summary:     "Logout and revoke tokens",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-current-user",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/me",
		Tags:        []string{"Auth"},
		Summary:     "Get current authenticated user",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body UserProfile }]())
}

func registerPublicContentOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-weather",
		Method:      http.MethodGet,
		Path:        "/api/v1/weather",
		Tags:        []string{"Weather"},
		Summary:     "Get current weather",
		Description: "Current conditions at the club from Yr.no. Cached 10 minutes.",
	}, stub[struct{}, struct{ Body WeatherResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "submit-contact-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/contact",
		Tags:        []string{"Contact"},
		Summary:     "Submit contact form",
	}, stub[struct{ Body ContactRequest }, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-legal-document",
		Method:      http.MethodGet,
		Path:        "/api/v1/legal/{docType}",
		Tags:        []string{"Legal"},
		Summary:     "Get legal document",
	}, stub[struct {
		DocType string `path:"docType" enum:"terms,privacy" doc:"Document type"`
	}, struct{ Body LegalDocument }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-public-pricing",
		Method:      http.MethodGet,
		Path:        "/api/v1/pricing",
		Tags:        []string{"Commerce"},
		Summary:     "List public pricing",
	}, stub[struct{}, struct{ Body []PriceItem }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-public-products",
		Method:      http.MethodGet,
		Path:        "/api/v1/products",
		Tags:        []string{"Commerce"},
		Summary:     "List public products",
	}, stub[struct{}, struct{ Body []Product }]())

	huma.Register(api, huma.Operation{
		OperationID: "search-boat-models",
		Method:      http.MethodGet,
		Path:        "/api/v1/boat-models",
		Tags:        []string{"Boats"},
		Summary:     "Search boat models",
	}, stub[struct {
		Query string `query:"q" doc:"Search query"`
	}, struct{ Body []BoatModel }]())
}

func registerMapOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-club-coordinates",
		Method:      http.MethodGet,
		Path:        "/api/v1/map/coordinates",
		Tags:        []string{"Map"},
		Summary:     "Get club coordinates",
	}, stub[struct{}, struct{ Body ClubCoordinatesResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-map-markers",
		Method:      http.MethodGet,
		Path:        "/api/v1/map/markers",
		Tags:        []string{"Map"},
		Summary:     "List map markers",
	}, stub[struct{}, struct{ Body []MapMarker }]())

	huma.Register(api, huma.Operation{
		OperationID: "export-gpx",
		Method:      http.MethodGet,
		Path:        "/api/v1/map/export/gpx",
		Tags:        []string{"Map"},
		Summary:     "Export markers as GPX",
		Description: "Downloads all markers and waypoints as a GPX file.",
	}, stub[struct{}, struct{}]())
}

func registerBookingOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-booking-resources",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/resources",
		Tags:        []string{"Bookings"},
		Summary:     "List bookable resources",
	}, stub[struct{}, struct{ Body []BookingResource }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-resource-availability",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/resources/{resourceID}/availability",
		Tags:        []string{"Bookings"},
		Summary:     "Get resource availability",
	}, stub[struct {
		ResourceID string `path:"resourceID" doc:"Resource UUID"`
		Start      string `query:"start" doc:"Start date (YYYY-MM-DD)"`
		End        string `query:"end" doc:"End date (YYYY-MM-DD)"`
	}, struct{ Body []DayAvailability }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-aggregate-availability",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/availability",
		Tags:        []string{"Bookings"},
		Summary:     "Aggregate availability by type",
	}, stub[struct {
		Type  string `query:"type" doc:"Resource type"`
		Start string `query:"start" doc:"Start date"`
		End   string `query:"end" doc:"End date"`
	}, struct{ Body []DayAvailability }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-today-availability",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/availability/today",
		Tags:        []string{"Bookings"},
		Summary:     "Today's availability",
	}, stub[struct {
		Type string `query:"type" doc:"Resource type"`
	}, struct{ Body TodayAvailability }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-hoist-slots",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/hoist/slots",
		Tags:        []string{"Bookings"},
		Summary:     "List hoist time slots",
	}, stub[struct {
		Date string `query:"date" doc:"Date (YYYY-MM-DD)"`
	}, struct{ Body HoistSlotsResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-booking",
		Method:      http.MethodPost,
		Path:        "/api/v1/bookings",
		Tags:        []string{"Bookings"},
		Summary:     "Create a booking",
		Description: "Create a booking. Authentication optional — guests provide contact info.",
	}, stub[struct{ Body CreateBookingRequest }, struct{ Body Booking }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-my-bookings",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/me",
		Tags:        []string{"Bookings"},
		Summary:     "List my bookings",
		Security:    BearerSecurity,
	}, stub[struct{ PaginationParams }, struct {
		Body PaginatedResponse[Booking]
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "get-booking",
		Method:      http.MethodGet,
		Path:        "/api/v1/bookings/{bookingID}",
		Tags:        []string{"Bookings"},
		Summary:     "Get booking details",
		Security:    BearerSecurity,
	}, stub[struct {
		BookingID string `path:"bookingID" doc:"Booking UUID"`
	}, struct{ Body Booking }]())

	huma.Register(api, huma.Operation{
		OperationID: "cancel-booking",
		Method:      http.MethodPost,
		Path:        "/api/v1/bookings/{bookingID}/cancel",
		Tags:        []string{"Bookings"},
		Summary:     "Cancel a booking",
		Security:    BearerSecurity,
	}, stub[struct {
		BookingID string `path:"bookingID" doc:"Booking UUID"`
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "confirm-booking",
		Method:      http.MethodPost,
		Path:        "/api/v1/bookings/{bookingID}/confirm",
		Tags:        []string{"Bookings"},
		Summary:     "Confirm a booking (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		BookingID string `path:"bookingID" doc:"Booking UUID"`
	}, struct{ Body StatusResponse }]())
}

func registerCalendarOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-public-events",
		Method:      http.MethodGet,
		Path:        "/api/v1/calendar",
		Tags:        []string{"Calendar"},
		Summary:     "List public events",
	}, stub[struct{}, struct{ Body []CalendarEvent }]())

	huma.Register(api, huma.Operation{
		OperationID: "export-calendar-ics",
		Method:      http.MethodGet,
		Path:        "/api/v1/calendar/public.ics",
		Tags:        []string{"Calendar"},
		Summary:     "Export calendar as ICS",
	}, stub[struct{}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "get-event",
		Method:      http.MethodGet,
		Path:        "/api/v1/calendar/{eventID}",
		Tags:        []string{"Calendar"},
		Summary:     "Get event details",
	}, stub[struct {
		EventID string `path:"eventID" doc:"Event UUID"`
	}, struct{ Body CalendarEvent }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-event",
		Method:      http.MethodPost,
		Path:        "/api/v1/calendar",
		Tags:        []string{"Calendar"},
		Summary:     "Create event (admin)",
		Security:    BearerSecurity,
	}, stub[struct{ Body CreateEventRequest }, struct{ Body CalendarEvent }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-event",
		Method:      http.MethodPut,
		Path:        "/api/v1/calendar/{eventID}",
		Tags:        []string{"Calendar"},
		Summary:     "Update event (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		EventID string `path:"eventID" doc:"Event UUID"`
		Body    CreateEventRequest
	}, struct{ Body CalendarEvent }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-event",
		Method:      http.MethodDelete,
		Path:        "/api/v1/calendar/{eventID}",
		Tags:        []string{"Calendar"},
		Summary:     "Delete event (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		EventID string `path:"eventID" doc:"Event UUID"`
	}, struct{}]())
}

func registerMemberOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-my-profile",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me",
		Tags:        []string{"Members"},
		Summary:     "Get my profile",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body MemberProfile }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-my-profile",
		Method:      http.MethodPut,
		Path:        "/api/v1/members/me",
		Tags:        []string{"Members"},
		Summary:     "Update my profile",
		Security:    BearerSecurity,
	}, stub[struct{ Body MemberProfile }, struct{ Body MemberProfile }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-dashboard",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/dashboard",
		Tags:        []string{"Members"},
		Summary:     "Get member dashboard",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body DashboardResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-my-boats",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/boats",
		Tags:        []string{"Members", "Boats"},
		Summary:     "List my boats",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []Boat }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-boat",
		Method:      http.MethodPost,
		Path:        "/api/v1/members/me/boats",
		Tags:        []string{"Members", "Boats"},
		Summary:     "Register a boat",
		Security:    BearerSecurity,
	}, stub[struct{ Body Boat }, struct{ Body Boat }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-boat",
		Method:      http.MethodPut,
		Path:        "/api/v1/members/me/boats/{boatID}",
		Tags:        []string{"Members", "Boats"},
		Summary:     "Update a boat",
		Security:    BearerSecurity,
	}, stub[struct {
		BoatID string `path:"boatID" doc:"Boat UUID"`
		Body   Boat
	}, struct{ Body Boat }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-boat",
		Method:      http.MethodDelete,
		Path:        "/api/v1/members/me/boats/{boatID}",
		Tags:        []string{"Members", "Boats"},
		Summary:     "Delete a boat",
		Security:    BearerSecurity,
	}, stub[struct {
		BoatID string `path:"boatID" doc:"Boat UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "get-my-slip",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/slip",
		Tags:        []string{"Members"},
		Summary:     "Get my slip assignment",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body MemberSlip }]())

	huma.Register(api, huma.Operation{
		OperationID: "report-slip-issue",
		Method:      http.MethodPost,
		Path:        "/api/v1/members/me/slip/issues",
		Tags:        []string{"Members"},
		Summary:     "Report a slip issue",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			Description string `json:"description" doc:"Issue description"`
		}
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-my-dugnad-hours",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/dugnad-hours",
		Tags:        []string{"Members", "Dugnad"},
		Summary:     "Get my volunteer hours",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body DugnadHoursSummary }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-member-directory",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/directory",
		Tags:        []string{"Members"},
		Summary:     "List member directory",
		Security:    BearerSecurity,
	}, stub[struct{}, struct {
		Body PaginatedResponse[DirectoryMember]
	}]())
}

func registerWaitingListOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "join-waiting-list",
		Method:      http.MethodPost,
		Path:        "/api/v1/waiting-list/join",
		Tags:        []string{"Waiting List"},
		Summary:     "Join the waiting list",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			BoatID *string `json:"boat_id,omitempty" doc:"Boat UUID to link"`
		}
	}, struct {
		Body struct {
			Position int              `json:"position" doc:"Queue position"`
			Entry    WaitingListEntry `json:"entry" doc:"Created entry"`
		}
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "get-my-waiting-list-position",
		Method:      http.MethodGet,
		Path:        "/api/v1/waiting-list/me",
		Tags:        []string{"Waiting List"},
		Summary:     "Get my position",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body WaitingListEntry }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-portal-waiting-list",
		Method:      http.MethodGet,
		Path:        "/api/v1/waiting-list/portal",
		Tags:        []string{"Waiting List"},
		Summary:     "View waiting list (portal)",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []PortalWaitingListEntry }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-waiting-list-boat",
		Method:      http.MethodPut,
		Path:        "/api/v1/waiting-list/me/boat",
		Tags:        []string{"Waiting List"},
		Summary:     "Update boat on waiting list entry",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			BoatID string `json:"boat_id" doc:"Boat UUID"`
		}
	}, struct{ Body WaitingListEntry }]())

	huma.Register(api, huma.Operation{
		OperationID: "withdraw-from-waiting-list",
		Method:      http.MethodPost,
		Path:        "/api/v1/waiting-list/withdraw",
		Tags:        []string{"Waiting List"},
		Summary:     "Withdraw from waiting list",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "accept-slip-offer",
		Method:      http.MethodPost,
		Path:        "/api/v1/waiting-list/{entryID}/accept",
		Tags:        []string{"Waiting List"},
		Summary:     "Accept slip offer",
		Security:    BearerSecurity,
	}, stub[struct {
		EntryID string `path:"entryID" doc:"Entry UUID"`
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-waiting-list",
		Method:      http.MethodGet,
		Path:        "/api/v1/waiting-list",
		Tags:        []string{"Waiting List"},
		Summary:     "List all entries (admin)",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []WaitingListEntryAdmin }]())

	huma.Register(api, huma.Operation{
		OperationID: "offer-slip",
		Method:      http.MethodPost,
		Path:        "/api/v1/waiting-list/{entryID}/offer",
		Tags:        []string{"Waiting List"},
		Summary:     "Offer slip to entry (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		EntryID string `path:"entryID" doc:"Entry UUID"`
		Body    struct {
			SlipID string `json:"slip_id" doc:"Slip UUID to offer"`
		}
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "reorder-waiting-list-entry",
		Method:      http.MethodPut,
		Path:        "/api/v1/waiting-list/{entryID}/position",
		Tags:        []string{"Waiting List"},
		Summary:     "Reorder entry (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		EntryID string `path:"entryID" doc:"Entry UUID"`
		Body    struct {
			Position int `json:"position" doc:"New position"`
		}
	}, struct{ Body WaitingListEntry }]())
}

func registerSlipShareOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-my-slip-shares",
		Method:      http.MethodGet,
		Path:        "/api/v1/portal/slip-shares",
		Tags:        []string{"Slip Shares"},
		Summary:     "List my slip share windows",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []SlipShare }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-slip-share",
		Method:      http.MethodPost,
		Path:        "/api/v1/portal/slip-shares",
		Tags:        []string{"Slip Shares"},
		Summary:     "Create slip share window",
		Security:    BearerSecurity,
	}, stub[struct{ Body SlipShare }, struct{ Body SlipShare }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-slip-share",
		Method:      http.MethodPut,
		Path:        "/api/v1/portal/slip-shares/{shareID}",
		Tags:        []string{"Slip Shares"},
		Summary:     "Update slip share window",
		Security:    BearerSecurity,
	}, stub[struct {
		ShareID string `path:"shareID" doc:"Share UUID"`
		Body    SlipShare
	}, struct{ Body SlipShare }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-slip-share",
		Method:      http.MethodDelete,
		Path:        "/api/v1/portal/slip-shares/{shareID}",
		Tags:        []string{"Slip Shares"},
		Summary:     "Cancel slip share window",
		Security:    BearerSecurity,
	}, stub[struct {
		ShareID string `path:"shareID" doc:"Share UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "list-my-rebates",
		Method:      http.MethodGet,
		Path:        "/api/v1/portal/slip-shares/rebates",
		Tags:        []string{"Slip Shares"},
		Summary:     "List my rebate history",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []SlipShareRebate }]())
}

func registerNotificationOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-vapid-key",
		Method:      http.MethodGet,
		Path:        "/api/v1/push/vapid-key",
		Tags:        []string{"Notifications"},
		Summary:     "Get VAPID public key",
		Security:    BearerSecurity,
	}, stub[struct{}, struct {
		Body struct {
			VAPIDKey string `json:"vapid_key" doc:"VAPID public key for push subscriptions"`
		}
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "subscribe-push",
		Method:      http.MethodPost,
		Path:        "/api/v1/push/subscribe",
		Tags:        []string{"Notifications"},
		Summary:     "Subscribe to push notifications",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "unsubscribe-push",
		Method:      http.MethodDelete,
		Path:        "/api/v1/push/subscribe",
		Tags:        []string{"Notifications"},
		Summary:     "Unsubscribe from push notifications",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-notification-preferences",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/notifications",
		Tags:        []string{"Notifications"},
		Summary:     "Get notification preferences",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []NotificationPreference }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-notification-preferences",
		Method:      http.MethodPut,
		Path:        "/api/v1/members/me/notifications",
		Tags:        []string{"Notifications"},
		Summary:     "Update notification preferences",
		Security:    BearerSecurity,
	}, stub[struct{ Body []NotificationPreference }, struct{ Body []NotificationPreference }]())
}

func registerForumOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-forum-rooms",
		Method:      http.MethodGet,
		Path:        "/api/v1/forum/rooms",
		Tags:        []string{"Forum"},
		Summary:     "List forum rooms",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-room-messages",
		Method:      http.MethodGet,
		Path:        "/api/v1/forum/rooms/{roomID}/messages",
		Tags:        []string{"Forum"},
		Summary:     "Get room messages",
		Security:    BearerSecurity,
	}, stub[struct {
		RoomID string `path:"roomID" doc:"Room ID"`
	}, struct{ Body []map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "send-message",
		Method:      http.MethodPost,
		Path:        "/api/v1/forum/rooms/{roomID}/messages",
		Tags:        []string{"Forum"},
		Summary:     "Send a message",
		Security:    BearerSecurity,
	}, stub[struct {
		RoomID string `path:"roomID" doc:"Room ID"`
		Body   map[string]any
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-room-members",
		Method:      http.MethodGet,
		Path:        "/api/v1/forum/rooms/{roomID}/members",
		Tags:        []string{"Forum"},
		Summary:     "List room members",
		Security:    BearerSecurity,
	}, stub[struct {
		RoomID string `path:"roomID" doc:"Room ID"`
	}, struct{ Body []map[string]any }]())
}

func registerProjectOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-projects",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects",
		Tags:        []string{"Projects"},
		Summary:     "List projects",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []ProjectWithCounts }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-project",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{projectID}",
		Tags:        []string{"Projects"},
		Summary:     "Get project details",
		Security:    BearerSecurity,
	}, stub[struct {
		ProjectID string `path:"projectID" doc:"Project UUID"`
	}, struct{ Body ProjectWithCounts }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-project-tasks",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{projectID}/tasks",
		Tags:        []string{"Projects"},
		Summary:     "List project tasks",
		Security:    BearerSecurity,
	}, stub[struct {
		ProjectID string `path:"projectID" doc:"Project UUID"`
	}, struct{ Body GroupedTasks }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-project",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects",
		Tags:        []string{"Projects"},
		Summary:     "Create project (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			Name        string `json:"name" doc:"Project name"`
			Description string `json:"description,omitempty" doc:"Project description"`
		}
	}, struct{ Body Project }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-task",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{projectID}/tasks",
		Tags:        []string{"Projects"},
		Summary:     "Create task (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		ProjectID string `path:"projectID" doc:"Project UUID"`
		Body      Task
	}, struct{ Body Task }]())

	// Task operations
	huma.Register(api, huma.Operation{
		OperationID: "join-task",
		Method:      http.MethodPost,
		Path:        "/api/v1/tasks/{taskID}/join",
		Tags:        []string{"Projects", "Dugnad"},
		Summary:     "Join a task",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "leave-task",
		Method:      http.MethodDelete,
		Path:        "/api/v1/tasks/{taskID}/leave",
		Tags:        []string{"Projects", "Dugnad"},
		Summary:     "Leave a task",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "list-task-participants",
		Method:      http.MethodGet,
		Path:        "/api/v1/tasks/{taskID}/participants",
		Tags:        []string{"Projects", "Dugnad"},
		Summary:     "List task participants",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
	}, struct{ Body []TaskParticipant }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-task",
		Method:      http.MethodPut,
		Path:        "/api/v1/tasks/{taskID}",
		Tags:        []string{"Projects"},
		Summary:     "Update task (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
		Body   Task
	}, struct{ Body Task }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-task",
		Method:      http.MethodDelete,
		Path:        "/api/v1/tasks/{taskID}",
		Tags:        []string{"Projects"},
		Summary:     "Delete task (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "assign-task",
		Method:      http.MethodPut,
		Path:        "/api/v1/tasks/{taskID}/assign",
		Tags:        []string{"Projects", "Dugnad"},
		Summary:     "Assign task to member (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
		Body   struct {
			AssigneeID string `json:"assignee_id" doc:"User UUID to assign"`
		}
	}, struct{ Body Task }]())

	huma.Register(api, huma.Operation{
		OperationID: "adjust-task-hours",
		Method:      http.MethodPut,
		Path:        "/api/v1/tasks/{taskID}/hours",
		Tags:        []string{"Projects", "Dugnad"},
		Summary:     "Adjust volunteer hours (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		TaskID string `path:"taskID" doc:"Task UUID"`
		Body   struct {
			UserID string  `json:"user_id" doc:"User UUID"`
			Hours  float64 `json:"hours" doc:"Hours to set"`
		}
	}, struct{ Body StatusResponse }]())
}

func registerShoppingListOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-shopping-lists",
		Method:      http.MethodGet,
		Path:        "/api/v1/shopping-lists",
		Tags:        []string{"Shopping Lists"},
		Summary:     "List shopping lists",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-shopping-list",
		Method:      http.MethodPost,
		Path:        "/api/v1/shopping-lists",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Create shopping list",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-shopping-list",
		Method:      http.MethodGet,
		Path:        "/api/v1/shopping-lists/{listID}",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Get shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-shopping-list",
		Method:      http.MethodPut,
		Path:        "/api/v1/shopping-lists/{listID}",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Update shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
		Body   map[string]any
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-shopping-list",
		Method:      http.MethodDelete,
		Path:        "/api/v1/shopping-lists/{listID}",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Delete shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "list-shopping-list-items",
		Method:      http.MethodGet,
		Path:        "/api/v1/shopping-lists/{listID}/items",
		Tags:        []string{"Shopping Lists"},
		Summary:     "List items in shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
	}, struct{ Body []map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "add-shopping-list-item",
		Method:      http.MethodPost,
		Path:        "/api/v1/shopping-lists/{listID}/items",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Add item to shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
		Body   map[string]any
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "populate-from-tasks",
		Method:      http.MethodPost,
		Path:        "/api/v1/shopping-lists/{listID}/from-tasks",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Populate list from project tasks",
		Security:    BearerSecurity,
	}, stub[struct {
		ListID string `path:"listID" doc:"List UUID"`
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "toggle-shopping-list-item",
		Method:      http.MethodPut,
		Path:        "/api/v1/shopping-lists/items/{itemID}/toggle",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Toggle item checked status",
		Security:    BearerSecurity,
	}, stub[struct {
		ItemID string `path:"itemID" doc:"Item UUID"`
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "delete-shopping-list-item",
		Method:      http.MethodDelete,
		Path:        "/api/v1/shopping-lists/items/{itemID}",
		Tags:        []string{"Shopping Lists"},
		Summary:     "Delete item from shopping list",
		Security:    BearerSecurity,
	}, stub[struct {
		ItemID string `path:"itemID" doc:"Item UUID"`
	}, struct{}]())
}

func registerFeatureRequestOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-feature-requests",
		Method:      http.MethodGet,
		Path:        "/api/v1/feature-requests",
		Tags:        []string{"Feature Requests"},
		Summary:     "List feature requests",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []FeatureRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "create-feature-request",
		Method:      http.MethodPost,
		Path:        "/api/v1/feature-requests",
		Tags:        []string{"Feature Requests"},
		Summary:     "Create feature request",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			Title       string `json:"title" doc:"Request title"`
			Description string `json:"description" doc:"Request description"`
		}
	}, struct{ Body FeatureRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-feature-request",
		Method:      http.MethodGet,
		Path:        "/api/v1/feature-requests/{requestID}",
		Tags:        []string{"Feature Requests"},
		Summary:     "Get feature request",
		Security:    BearerSecurity,
	}, stub[struct {
		RequestID string `path:"requestID" doc:"Request UUID"`
	}, struct{ Body FeatureRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "vote-feature-request",
		Method:      http.MethodPost,
		Path:        "/api/v1/feature-requests/{requestID}/vote",
		Tags:        []string{"Feature Requests"},
		Summary:     "Vote on feature request",
		Security:    BearerSecurity,
	}, stub[struct {
		RequestID string `path:"requestID" doc:"Request UUID"`
		Body      struct {
			Value int `json:"value" doc:"Vote value (1 or -1)"`
		}
	}, struct{ Body FeatureRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-feature-request-status",
		Method:      http.MethodPut,
		Path:        "/api/v1/feature-requests/{requestID}/status",
		Tags:        []string{"Feature Requests"},
		Summary:     "Update status (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		RequestID string `path:"requestID" doc:"Request UUID"`
		Body      struct {
			Status string `json:"status" doc:"New status"`
		}
	}, struct{ Body FeatureRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "promote-to-task",
		Method:      http.MethodPost,
		Path:        "/api/v1/feature-requests/{requestID}/promote",
		Tags:        []string{"Feature Requests"},
		Summary:     "Promote to project task (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		RequestID string `path:"requestID" doc:"Request UUID"`
	}, struct{ Body Task }]())
}

func registerOrderOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-order",
		Method:      http.MethodPost,
		Path:        "/api/v1/orders",
		Tags:        []string{"Commerce"},
		Summary:     "Create an order",
	}, stub[struct{ Body map[string]any }, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-order",
		Method:      http.MethodGet,
		Path:        "/api/v1/orders/{orderID}",
		Tags:        []string{"Commerce"},
		Summary:     "Get order details",
	}, stub[struct {
		OrderID string `path:"orderID" doc:"Order UUID"`
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "confirm-order",
		Method:      http.MethodPost,
		Path:        "/api/v1/orders/{orderID}/confirm",
		Tags:        []string{"Commerce"},
		Summary:     "Confirm order payment",
	}, stub[struct {
		OrderID string `path:"orderID" doc:"Order UUID"`
	}, struct{ Body StatusResponse }]())
}

func registerGDPROps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "export-my-data",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/data-export",
		Tags:        []string{"GDPR"},
		Summary:     "Export my data",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body map[string]any }]()) // data export is dynamic

	huma.Register(api, huma.Operation{
		OperationID: "request-deletion",
		Method:      http.MethodPost,
		Path:        "/api/v1/members/me/delete-request",
		Tags:        []string{"GDPR"},
		Summary:     "Request account deletion",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body DeletionRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "cancel-deletion",
		Method:      http.MethodDelete,
		Path:        "/api/v1/members/me/delete-request",
		Tags:        []string{"GDPR"},
		Summary:     "Cancel deletion request",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-deletion-status",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/delete-request",
		Tags:        []string{"GDPR"},
		Summary:     "Get deletion request status",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body DeletionRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "record-consent",
		Method:      http.MethodPost,
		Path:        "/api/v1/members/me/consent",
		Tags:        []string{"GDPR"},
		Summary:     "Record consent",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			ConsentType string `json:"consent_type" doc:"Type of consent"`
			Version     string `json:"version" doc:"Consent version"`
		}
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-my-consents",
		Method:      http.MethodGet,
		Path:        "/api/v1/members/me/consents",
		Tags:        []string{"GDPR"},
		Summary:     "List my consents",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []Consent }]())
}

func registerAdminOps(api huma.API) {
	// Documents
	huma.Register(api, huma.Operation{
		OperationID: "list-documents",
		Method:      http.MethodGet,
		Path:        "/api/v1/documents",
		Tags:        []string{"Documents"},
		Summary:     "List documents",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []Document }]())

	huma.Register(api, huma.Operation{
		OperationID: "get-document",
		Method:      http.MethodGet,
		Path:        "/api/v1/documents/{docID}",
		Tags:        []string{"Documents"},
		Summary:     "Get document",
		Security:    BearerSecurity,
	}, stub[struct {
		DocID string `path:"docID" doc:"Document UUID"`
	}, struct{ Body Document }]())

	// Audit
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/audit",
		Tags:        []string{"Admin"},
		Summary:     "List audit log entries",
		Security:    BearerSecurity,
	}, stub[struct{ PaginationParams }, struct {
		Body PaginatedResponse[map[string]any]
	}]())

	// Admin Users
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/users",
		Tags:        []string{"Admin"},
		Summary:     "List all users",
		Security:    BearerSecurity,
	}, stub[struct{ PaginationParams }, struct {
		Body AdminUsersResponse
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-user",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/users/{userID}",
		Tags:        []string{"Admin"},
		Summary:     "Get user details",
		Security:    BearerSecurity,
	}, stub[struct {
		UserID string `path:"userID" doc:"User UUID"`
	}, struct{ Body AdminUser }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-user-roles",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/users/{userID}/roles",
		Tags:        []string{"Admin"},
		Summary:     "Update user roles",
		Security:    BearerSecurity,
	}, stub[struct {
		UserID string `path:"userID" doc:"User UUID"`
		Body   struct {
			Roles []string `json:"roles" doc:"Role names to assign"`
		}
	}, struct{ Body AdminUser }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-user",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/users/{userID}",
		Tags:        []string{"Admin"},
		Summary:     "Delete user",
		Security:    BearerSecurity,
	}, stub[struct {
		UserID string `path:"userID" doc:"User UUID"`
	}, struct{}]())

	// Admin Slips
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-slips",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/slips",
		Tags:        []string{"Admin"},
		Summary:     "List all slips",
		Security:    BearerSecurity,
	}, stub[struct{ PaginationParams }, struct {
		Body PaginatedResponse[Slip]
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-create-slip",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/slips",
		Tags:        []string{"Admin"},
		Summary:     "Create slip",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body Slip }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-slip",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/slips/{slipID}",
		Tags:        []string{"Admin"},
		Summary:     "Get slip details",
		Security:    BearerSecurity,
	}, stub[struct {
		SlipID string `path:"slipID" doc:"Slip UUID"`
	}, struct{ Body Slip }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-slip",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/slips/{slipID}",
		Tags:        []string{"Admin"},
		Summary:     "Update slip",
		Security:    BearerSecurity,
	}, stub[struct {
		SlipID string `path:"slipID" doc:"Slip UUID"`
		Body   map[string]any
	}, struct{ Body Slip }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-assign-slip",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/slips/{slipID}/assign",
		Tags:        []string{"Admin"},
		Summary:     "Assign slip to member",
		Security:    BearerSecurity,
	}, stub[struct {
		SlipID string `path:"slipID" doc:"Slip UUID"`
		Body   map[string]any
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-release-slip",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/slips/{slipID}/release",
		Tags:        []string{"Admin"},
		Summary:     "Release slip assignment",
		Security:    BearerSecurity,
	}, stub[struct {
		SlipID string `path:"slipID" doc:"Slip UUID"`
	}, struct{ Body StatusResponse }]())

	// Admin Bookings
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-bookings",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/bookings",
		Tags:        []string{"Admin"},
		Summary:     "List all bookings (admin)",
		Security:    BearerSecurity,
	}, stub[struct {
		PaginationParams
		Status       string `query:"status,omitempty" doc:"Filter by status"`
		ResourceType string `query:"resource_type,omitempty" doc:"Filter by resource type"`
		Start        string `query:"start,omitempty" doc:"Filter from date"`
		End          string `query:"end,omitempty" doc:"Filter to date"`
	}, struct {
		Body PaginatedResponse[BookingAdmin]
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "get-booking-settings",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/settings/booking",
		Tags:        []string{"Admin"},
		Summary:     "Get booking settings",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "update-booking-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/settings/booking",
		Tags:        []string{"Admin"},
		Summary:     "Update booking settings",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body map[string]any }]())

	// Admin Slip Shares
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-slip-shares",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/slip-shares",
		Tags:        []string{"Admin", "Slip Shares"},
		Summary:     "List all slip shares",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []SlipShareAdmin }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-rebates",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/slip-shares/rebates",
		Tags:        []string{"Admin", "Slip Shares"},
		Summary:     "List all rebates",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []SlipShareRebate }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-rebate-status",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/slip-shares/rebates/{rebateID}",
		Tags:        []string{"Admin", "Slip Shares"},
		Summary:     "Update rebate status",
		Security:    BearerSecurity,
	}, stub[struct {
		RebateID string `path:"rebateID" doc:"Rebate UUID"`
		Body     struct {
			Status string `json:"status" doc:"New rebate status"`
		}
	}, struct{ Body SlipShareRebate }]())

	// Admin Documents
	huma.Register(api, huma.Operation{
		OperationID: "admin-upload-document",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/documents",
		Tags:        []string{"Admin", "Documents"},
		Summary:     "Upload document",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body Document }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-document",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/documents/{docID}",
		Tags:        []string{"Admin", "Documents"},
		Summary:     "Delete document",
		Security:    BearerSecurity,
	}, stub[struct {
		DocID string `path:"docID" doc:"Document UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "summarize-document-comments",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/documents/{docID}/summarize",
		Tags:        []string{"Admin", "Documents"},
		Summary:     "AI-summarize document comments",
		Security:    BearerSecurity,
	}, stub[struct {
		DocID string `path:"docID" doc:"Document UUID"`
	}, struct{ Body map[string]any }]())

	huma.Register(api, huma.Operation{
		OperationID: "generate-meeting-agenda",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/documents/{docID}/sakliste",
		Tags:        []string{"Admin", "Documents"},
		Summary:     "Generate meeting agenda from comments",
		Security:    BearerSecurity,
	}, stub[struct {
		DocID string `path:"docID" doc:"Document UUID"`
	}, struct{ Body map[string]any }]())

	// Admin Boats
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-unconfirmed-boats",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/boats/unconfirmed",
		Tags:        []string{"Admin", "Boats"},
		Summary:     "List unconfirmed boats",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []UnconfirmedBoat }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-confirm-boat",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/boats/{boatID}/confirm",
		Tags:        []string{"Admin", "Boats"},
		Summary:     "Confirm boat registration",
		Security:    BearerSecurity,
	}, stub[struct {
		BoatID string `path:"boatID" doc:"Boat UUID"`
	}, struct{ Body StatusResponse }]())

	// Admin Map Markers
	huma.Register(api, huma.Operation{
		OperationID: "admin-create-marker",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/map/markers",
		Tags:        []string{"Admin", "Map"},
		Summary:     "Create map marker",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body MapMarker }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-marker",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/map/markers/{markerID}",
		Tags:        []string{"Admin", "Map"},
		Summary:     "Update map marker",
		Security:    BearerSecurity,
	}, stub[struct {
		MarkerID string `path:"markerID" doc:"Marker UUID"`
		Body     map[string]any
	}, struct{ Body MapMarker }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-marker",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/map/markers/{markerID}",
		Tags:        []string{"Admin", "Map"},
		Summary:     "Delete map marker",
		Security:    BearerSecurity,
	}, stub[struct {
		MarkerID string `path:"markerID" doc:"Marker UUID"`
	}, struct{}]())

	// Admin Broadcast
	huma.Register(api, huma.Operation{
		OperationID: "admin-send-broadcast",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/broadcast",
		Tags:        []string{"Admin"},
		Summary:     "Send broadcast message",
		Security:    BearerSecurity,
	}, stub[struct{ Body map[string]any }, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-broadcasts",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/broadcasts",
		Tags:        []string{"Admin"},
		Summary:     "List sent broadcasts",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []Broadcast }]())

	// Admin Financials
	huma.Register(api, huma.Operation{
		OperationID: "admin-financial-summary",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/financials/summary",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "Get financial summary",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body FinancialSummary }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-payments",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/financials/payments",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "List payments",
		Security:    BearerSecurity,
	}, stub[struct{ PaginationParams }, struct {
		Body PaymentsListResponse
	}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-payment",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/financials/payments/{paymentID}",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "Get payment details",
		Security:    BearerSecurity,
	}, stub[struct {
		PaymentID string `path:"paymentID" doc:"Payment UUID"`
	}, struct{ Body Payment }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-export-financials",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/financials/export",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "Export financials as CSV",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-generate-invoice",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/financials/invoices",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "Generate invoice",
		Security:    BearerSecurity,
	}, stub[struct{ Body CreateInvoiceRequest }, struct{ Body Payment }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-overdue",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/financials/overdue",
		Tags:        []string{"Admin", "Financials"},
		Summary:     "List overdue payments",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []OverduePayment }]())

	// Admin Pricing
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-pricing",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/pricing",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "List pricing items (admin)",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []PriceItem }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-create-pricing",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/pricing",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Create pricing item",
		Security:    BearerSecurity,
	}, stub[struct{ Body PriceItem }, struct{ Body PriceItem }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-pricing",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/pricing/{itemID}",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Update pricing item",
		Security:    BearerSecurity,
	}, stub[struct {
		ItemID string `path:"itemID" doc:"Item UUID"`
		Body   PriceItem
	}, struct{ Body PriceItem }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-pricing",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/pricing/{itemID}",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Delete pricing item",
		Security:    BearerSecurity,
	}, stub[struct {
		ItemID string `path:"itemID" doc:"Item UUID"`
	}, struct{}]())

	// Admin Products
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-products",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/products",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "List products (admin)",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []Product }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-create-product",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/products",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Create product",
		Security:    BearerSecurity,
	}, stub[struct{ Body Product }, struct{ Body Product }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-product",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/products/{productID}",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Update product",
		Security:    BearerSecurity,
	}, stub[struct {
		ProductID string `path:"productID" doc:"Product UUID"`
		Body      Product
	}, struct{ Body Product }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-product",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/products/{productID}",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Delete product",
		Security:    BearerSecurity,
	}, stub[struct {
		ProductID string `path:"productID" doc:"Product UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-create-variant",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/products/{productID}/variants",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Create product variant",
		Security:    BearerSecurity,
	}, stub[struct {
		ProductID string `path:"productID" doc:"Product UUID"`
		Body      ProductVariant
	}, struct{ Body ProductVariant }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-variant",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/products/variants/{variantID}",
		Tags:        []string{"Admin", "Commerce"},
		Summary:     "Delete product variant",
		Security:    BearerSecurity,
	}, stub[struct {
		VariantID string `path:"variantID" doc:"Variant UUID"`
	}, struct{}]())

	// Admin Notifications
	huma.Register(api, huma.Operation{
		OperationID: "admin-get-notification-config",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/notifications/config",
		Tags:        []string{"Admin", "Notifications"},
		Summary:     "Get notification config",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []NotificationConfig }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-update-notification-config",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/notifications/config",
		Tags:        []string{"Admin", "Notifications"},
		Summary:     "Update notification config",
		Security:    BearerSecurity,
	}, stub[struct{ Body []NotificationConfig }, struct{ Body []NotificationConfig }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-test-push",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/notifications/test",
		Tags:        []string{"Admin", "Notifications"},
		Summary:     "Send test push notification",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body StatusResponse }]())

	// Admin GDPR
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-deletion-requests",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/gdpr/deletion-requests",
		Tags:        []string{"Admin", "GDPR"},
		Summary:     "List deletion requests",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []DeletionRequest }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-process-deletion",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/gdpr/deletion-requests/{requestID}/process",
		Tags:        []string{"Admin", "GDPR"},
		Summary:     "Process deletion request",
		Security:    BearerSecurity,
	}, stub[struct {
		RequestID string `path:"requestID" doc:"Request UUID"`
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-legal-documents",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/gdpr/legal",
		Tags:        []string{"Admin", "GDPR"},
		Summary:     "List legal documents (admin)",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []LegalDocument }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-create-legal-document",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/gdpr/legal",
		Tags:        []string{"Admin", "GDPR"},
		Summary:     "Create/update legal document",
		Security:    BearerSecurity,
	}, stub[struct{ Body LegalDocument }, struct{ Body LegalDocument }]())

	// Admin Dugnad
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-dugnad-hours",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/dugnad/hours",
		Tags:        []string{"Admin", "Dugnad"},
		Summary:     "List all volunteer hours",
		Security:    BearerSecurity,
	}, stub[struct{}, struct{ Body []DugnadHoursSummary }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-set-required-hours",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/dugnad/settings/hours",
		Tags:        []string{"Admin", "Dugnad"},
		Summary:     "Set required volunteer hours",
		Security:    BearerSecurity,
	}, stub[struct {
		Body struct {
			RequiredHours float64 `json:"required_hours" doc:"Required hours per member"`
		}
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-link-project-event",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/dugnad/events/{eventID}/projects",
		Tags:        []string{"Admin", "Dugnad"},
		Summary:     "Link project to event",
		Security:    BearerSecurity,
	}, stub[struct {
		EventID string `path:"eventID" doc:"Event UUID"`
		Body    struct {
			ProjectID string `json:"project_id" doc:"Project UUID"`
		}
	}, struct{ Body StatusResponse }]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-unlink-project-event",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/dugnad/events/{eventID}/projects/{projectID}",
		Tags:        []string{"Admin", "Dugnad"},
		Summary:     "Unlink project from event",
		Security:    BearerSecurity,
	}, stub[struct {
		EventID   string `path:"eventID" doc:"Event UUID"`
		ProjectID string `path:"projectID" doc:"Project UUID"`
	}, struct{}]())

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-event-projects",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/dugnad/events/{eventID}/projects",
		Tags:        []string{"Admin", "Dugnad"},
		Summary:     "List projects linked to event",
		Security:    BearerSecurity,
	}, stub[struct {
		EventID string `path:"eventID" doc:"Event UUID"`
	}, struct{ Body []Project }]())
}
