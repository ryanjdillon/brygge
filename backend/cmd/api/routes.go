package main

import (
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/devtoken"
	"github.com/brygge-klubb/brygge/internal/handlers"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/middleware"
	oa "github.com/brygge-klubb/brygge/internal/openapi"
)

// routeDeps is what the HTTP routes are wired from. main builds it from the
// live services; the route-table tests build it with inert handlers, so they
// walk the same router the API serves.
type routeDeps struct {
	cfg             *config.Config
	log             zerolog.Logger
	db              *pgxpool.Pool
	rdb             *redis.Client
	trustedProxies  []netip.Prefix
	sessionService  *auth.SessionService
	auditService    *audit.Service
	devTokenService *devtoken.Service
	deliveryStore   *broadcast.Store
	deliveryWorker  *broadcast.Worker
	inboxReconciler *mail.Reconciler
	userProvisioner *mail.UserProvisioner
	// wrapOptionalSession, when set, wraps the optional-session middleware:
	// the route-table test uses it to see what those routes are handed.
	wrapOptionalSession     func(func(http.Handler) http.Handler) func(http.Handler) http.Handler
	accountingHandler       *handlers.AccountingHandler
	adminDocumentsHandler   *handlers.AdminDocumentsHandler
	adminSlipsHandler       *handlers.AdminSlipsHandler
	adminUsersHandler       *handlers.AdminUsersHandler
	aiDocumentsHandler      *handlers.AIDocumentsHandler
	auditHandler            *handlers.AuditHandler
	authHandler             *handlers.AuthHandler
	bankAccountsHandler     *handlers.BankAccountsHandler
	bankRowsHandler         *handlers.BankRowsHandler
	boatModelsHandler       *handlers.BoatModelsHandler
	bookingsHandler         *handlers.BookingsHandler
	broadcastsHandler       *handlers.BroadcastsHandler
	calendarHandler         *handlers.CalendarHandler
	clubSettingsHandler     *handlers.ClubSettingsHandler
	contactHandler          *handlers.ContactHandler
	contentDocumentsHandler *handlers.ContentDocumentsHandler
	demoAuthHandler         *handlers.DemoAuthHandler
	devQueryHandler         *handlers.DevQueryHandler
	devTokenHandler         *handlers.DevTokenHandler
	emailPrefsHandler       *handlers.EmailPrefsHandler
	faqsHandler             *handlers.FaqsHandler
	featureRequestsHandler  *handlers.FeatureRequestsHandler
	featuresHandler         *handlers.FeaturesHandler
	feedbackHandler         *handlers.FeedbackHandler
	financialsHandler       *handlers.FinancialsHandler
	gdprHandler             *handlers.GDPRHandler
	harborHandler           *handlers.HarborHandler
	healthHandler           *handlers.HealthHandler
	invoiceHandler          *handlers.InvoiceHandler
	joinHandler             *handlers.JoinHandler
	magicLinkHandler        *handlers.MagicLinkHandler
	mapHandler              *handlers.MapHandler
	membersHandler          *handlers.MembersHandler
	notificationsHandler    *handlers.NotificationsHandler
	orderRefundHandler      *handlers.OrderRefundHandler
	passkeyHandler          *handlers.PasskeyHandler
	paymentFilterHandler    *handlers.PaymentFilterHandler
	priceItemsHandler       *handlers.PriceItemsHandler
	productsHandler         *handlers.ProductsHandler
	projectsHandler         *handlers.ProjectsHandler
	shoppingListsHandler    *handlers.ShoppingListsHandler
	slipSharesHandler       *handlers.SlipSharesHandler
	stripeWebhookHandler    *handlers.StripeWebhookHandler
	totpHandler             *handlers.TOTPHandler
	vippsWebhookHandler     *handlers.VippsWebhookHandler
	volunteerHandler        *handlers.VolunteerHandler
	waitingListHandler      *handlers.WaitingListHandler
	weatherHandler          *handlers.WeatherHandler
}

// newRouter mounts every API route, the API docs and the short-code redirect.
// The SPA fallback is added by the caller, after these.
func newRouter(d routeDeps) *chi.Mux {
	optionalSession := middleware.OptionalSessionAuth(d.sessionService)
	if d.wrapOptionalSession != nil {
		optionalSession = d.wrapOptionalSession(optionalSession)
	}
	cfg := d.cfg
	log := d.log
	db := d.db
	rdb := d.rdb
	trustedProxies := d.trustedProxies
	sessionService := d.sessionService
	auditService := d.auditService
	devTokenService := d.devTokenService
	deliveryStore := d.deliveryStore
	deliveryWorker := d.deliveryWorker
	inboxReconciler := d.inboxReconciler
	userProvisioner := d.userProvisioner
	accountingHandler := d.accountingHandler
	adminDocumentsHandler := d.adminDocumentsHandler
	adminSlipsHandler := d.adminSlipsHandler
	adminUsersHandler := d.adminUsersHandler
	aiDocumentsHandler := d.aiDocumentsHandler
	auditHandler := d.auditHandler
	authHandler := d.authHandler
	bankAccountsHandler := d.bankAccountsHandler
	bankRowsHandler := d.bankRowsHandler
	boatModelsHandler := d.boatModelsHandler
	bookingsHandler := d.bookingsHandler
	broadcastsHandler := d.broadcastsHandler
	calendarHandler := d.calendarHandler
	clubSettingsHandler := d.clubSettingsHandler
	contactHandler := d.contactHandler
	contentDocumentsHandler := d.contentDocumentsHandler
	demoAuthHandler := d.demoAuthHandler
	devQueryHandler := d.devQueryHandler
	devTokenHandler := d.devTokenHandler
	emailPrefsHandler := d.emailPrefsHandler
	faqsHandler := d.faqsHandler
	featureRequestsHandler := d.featureRequestsHandler
	featuresHandler := d.featuresHandler
	feedbackHandler := d.feedbackHandler
	financialsHandler := d.financialsHandler
	gdprHandler := d.gdprHandler
	harborHandler := d.harborHandler
	healthHandler := d.healthHandler
	invoiceHandler := d.invoiceHandler
	joinHandler := d.joinHandler
	magicLinkHandler := d.magicLinkHandler
	mapHandler := d.mapHandler
	membersHandler := d.membersHandler
	notificationsHandler := d.notificationsHandler
	orderRefundHandler := d.orderRefundHandler
	passkeyHandler := d.passkeyHandler
	paymentFilterHandler := d.paymentFilterHandler
	priceItemsHandler := d.priceItemsHandler
	productsHandler := d.productsHandler
	projectsHandler := d.projectsHandler
	shoppingListsHandler := d.shoppingListsHandler
	slipSharesHandler := d.slipSharesHandler
	stripeWebhookHandler := d.stripeWebhookHandler
	totpHandler := d.totpHandler
	vippsWebhookHandler := d.vippsWebhookHandler
	volunteerHandler := d.volunteerHandler
	waitingListHandler := d.waitingListHandler
	weatherHandler := d.weatherHandler

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(middleware.ClientIP(trustedProxies))
	r.Use(requestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(middleware.Metrics)
	r.Use(securityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{cfg.FrontendURL},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	strictRL := middleware.RateLimitByIP(rdb, log, 5, time.Minute)
	standardRL := middleware.RateLimitByIP(rdb, log, 30, time.Minute)
	authedRL := middleware.RateLimitByUser(rdb, log, 120, time.Minute)

	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/health", healthHandler)
		r.Get("/features", featuresHandler.HandleGetFeatures)

		// BRY-115: the dev-query endpoint for developer bearer tokens, so an
		// external tool can query without a cookie session or SSH. Mounted
		// only when the club opts in. Its sole auth is the token (cookies are
		// ignored); the per-IP limiter blunts token guessing and the
		// per-token limiter throttles a leaked token. Same handler, and so
		// the same brygge_dev_ro boundary, as /admin/dev/query.
		if cfg.DevQueryTokensEnabled {
			r.Group(func(r chi.Router) {
				r.Use(standardRL)
				r.Use(middleware.RequireDevToken(devTokenService, devtoken.ScopeDBQuery))
				r.Use(middleware.NewRateLimiter(rdb, log, 60, time.Minute, middleware.DevTokenKey).Handler)
				r.Post("/dev/query", devQueryHandler.HandleQuery)
			})
		}

		r.Route("/auth", func(r chi.Router) {
			if cfg.Features.DemoAuth {
				r.Get("/demo/users", demoAuthHandler.HandleListDemoUsers)
				r.Post("/demo/login", demoAuthHandler.HandleDemoLogin)
			}

			r.Group(func(r chi.Router) {
				r.Use(strictRL)
				r.Post("/magic-link", magicLinkHandler.HandleRequestMagicLink)
				r.Get("/verify", magicLinkHandler.HandleVerifyMagicLink)
			})

			r.Group(func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))
				r.Use(authedRL)
				r.Get("/me", authHandler.HandleMe)
			})

			// Session-based auth (new)
			r.Group(func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))
				r.Post("/session/logout", magicLinkHandler.HandleSessionLogout)
				r.Get("/session/me", authHandler.HandleMe) // same handler — reads from context
			})

			// Passkeys (WebAuthn).
			r.Route("/passkeys", func(r chi.Router) {
				// Discoverable login — public, no session yet, IP rate-limited
				// like magic-link. Terminates in the same session creation.
				r.Group(func(r chi.Router) {
					r.Use(strictRL)
					r.Post("/login/begin", passkeyHandler.HandleLoginBegin)
					r.Post("/login/finish", passkeyHandler.HandleLoginFinish)
				})

				// Everything else needs a session. Enroll/remove is a step-up
				// action (fresh TOTP); list/rename/step-up only need a session.
				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthenticateSession(sessionService))
					r.Use(authedRL)
					r.Get("/", passkeyHandler.HandleList)
					r.Patch("/{id}", passkeyHandler.HandleRename)

					r.Post("/step-up/begin", passkeyHandler.HandleStepUpBegin)
					r.Post("/step-up/finish", passkeyHandler.HandleStepUpFinish)

					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireFreshTOTPDefault())
						r.Post("/register/begin", passkeyHandler.HandleRegisterBegin)
						r.Post("/register/finish", passkeyHandler.HandleRegisterFinish)
						r.Delete("/{id}", passkeyHandler.HandleDelete)
					})
				})
			})
		})

		// The contact form is public and sends a real email on every
		// submission, so it gets the strict budget rather than the standard
		// one (BRY-475). Its own key keeps a visitor hammering the form from
		// using up the IP's strict budget for magic-link login. RemoteAddr
		// is already the client IP here (middleware.ClientIP).
		contactRL := middleware.NewRateLimiter(rdb, log, 5, time.Minute, func(r *http.Request) string {
			return "contact:" + r.RemoteAddr
		})
		r.Group(func(r chi.Router) {
			r.Use(contactRL.Handler)
			r.Post("/contact", contactHandler.HandleContactForm)
		})

		// Self-service join (BRY-629). POST /join sends an email to an
		// address the visitor chose, so it has its own strict per-IP budget
		// that fails closed: a Redis outage refuses joins rather than lifting
		// the limit. The handler adds a per-recipient throttle on top. The
		// confirm routes are token-addressed and cost no mail; their own key
		// keeps them off the magic-link budget.
		joinRL := middleware.NewRateLimiter(rdb, log, 5, time.Minute, func(r *http.Request) string {
			return "join:" + r.RemoteAddr
		}).FailClosed()
		joinConfirmRL := middleware.NewRateLimiter(rdb, log, 10, time.Minute, func(r *http.Request) string {
			return "join-confirm:" + r.RemoteAddr
		})
		r.With(standardRL).Get("/join/status", joinHandler.HandleJoinStatus)
		r.With(joinRL.Handler).Post("/join", joinHandler.HandleJoin)
		r.With(joinConfirmRL.Handler).Get("/join/confirm/{token}", joinHandler.HandleJoinConfirmStatus)
		r.With(joinConfirmRL.Handler).Post("/join/confirm", joinHandler.HandleJoinConfirm)
		// "Betal med bank" (BRY-631): the signed-in applicant's pending join
		// order becomes a KID faktura. Listed in middleware.ApplicantRoutes.
		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Use(authedRL)
			r.Post("/join/bank-invoice", invoiceHandler.HandleJoinBankInvoice)
		})

		r.Group(func(r chi.Router) {
			r.Use(standardRL)
			// Pricing belongs to the Accounting module (membership dues,
			// slip fees, electricity per kWh, etc.) — Commerce is just
			// the merchandise shop now.
			if cfg.Features.Accounting {
				r.Get("/pricing", priceItemsHandler.HandleListPublic)
			}
			if cfg.Features.Commerce {
				r.Get("/products", productsHandler.HandleListPublic)
			}
			r.Get("/boat-models", boatModelsHandler.HandleSearch)
			r.Get("/faqs", faqsHandler.HandleListPublic)
			r.Get("/weather", weatherHandler.HandleGetWeather)
			r.Get("/unsubscribe", emailPrefsHandler.HandleUnsubscribeRedirect)
			r.Post("/unsubscribe", emailPrefsHandler.HandleUnsubscribeOneClick)
			// One-tap pay link from older faktura emails: mints a payment with
			// the only configured provider, or sends the payer to the
			// /betal/{token} page to choose when there are several. New emails
			// link to that page directly.
			r.Get("/betal/{token}", invoiceHandler.HandleOrderPayRedirect)
			// Public per-member payables page (BRY-275): a durable personal token
			// lists the member's open fakturas; paying one reuses /betal/{token}.
			r.Get("/betal/bruker/{token}", invoiceHandler.HandleGetPayables)
			// Token-addressed checkout page API: itemised faktura + ad-hoc catalog
			// add-ons, then mint a payment with the payer's provider on demand.
			r.Get("/checkout/{token}", invoiceHandler.HandleGetCheckout)
			r.Post("/checkout/{token}/lines", invoiceHandler.HandleAddCheckoutLine)
			r.Delete("/checkout/{token}/lines/{lineID}", invoiceHandler.HandleRemoveCheckoutLine)
			r.Post("/checkout/{token}/pay", invoiceHandler.HandlePayCheckout)
			// Public harbor QR checkout (BRY-6): a poster's QR encodes /qr/{token};
			// the payer picks nights/units and pays a fresh ePayment.
			r.Get("/qr/{token}", invoiceHandler.HandleGetQRTarget)
			r.Post("/qr/{token}/pay", invoiceHandler.HandlePayQRTarget)
			r.Get("/club", clubSettingsHandler.HandleGetPublicClub)
			r.Get("/club/logo", clubSettingsHandler.HandleGetPublicClubLogo)
			// BIMI validators (and many receivers) want the indicator
			// URL to end with `.svg` so they can short-circuit on
			// extension before fetching. Same handler, just a path
			// alias so we can publish the friendlier URL in the BIMI
			// DNS record.
			r.Get("/club/logo.svg", clubSettingsHandler.HandleGetPublicClubLogo)
		})

		r.Get("/legal/{docType}", gdprHandler.HandleGetLegalDocument)

		r.Route("/map", func(r chi.Router) {
			r.Get("/coordinates", mapHandler.HandleGetClubCoordinates)
			r.Get("/markers", mapHandler.HandleListMarkers)
			r.Get("/export/gpx", mapHandler.HandleExportGPX)
		})

		r.Group(func(r chi.Router) {
			r.Use(optionalSession)
			r.Get("/harbor/layout", harborHandler.HandleGetLayout)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Use(middleware.RequireRoleOrBoardRead("board", "admin", "harbor_master"))
			r.Put("/harbor/layout", harborHandler.HandlePutLayout)
			// Near-realtime harbor overview of recent paid orders (BRY-265).
			r.Get("/harbor/recent-payments", harborHandler.HandleRecentPayments)
			// Harbor QR payment targets — mint/manage posters (BRY-6).
			r.Get("/harbor/payment-targets", harborHandler.HandleListTargets)
			r.Post("/harbor/payment-targets", harborHandler.HandleCreateTarget)
			r.Patch("/harbor/payment-targets/{id}", harborHandler.HandleUpdateTarget)
			r.Post("/harbor/payment-targets/{id}/archive", harborHandler.HandleArchiveTarget)
			r.Get("/harbor/payment-targets/{id}/qr.png", harborHandler.HandleTargetQR)
			r.Get("/harbor/payment-targets/{id}/poster.pdf", harborHandler.HandleTargetPoster)
		})

		// Vipps webhook — public endpoint, authenticated by HMAC-SHA256 signature.
		r.Post("/vipps/webhooks", vippsWebhookHandler.Handle)
		// Stripe webhook — public, authenticated by the Stripe-Signature HMAC.
		// No rate limiter: Stripe retries in bursts and the signature is the gate.
		r.Post("/stripe/webhooks", stripeWebhookHandler.Handle)

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/documents", adminDocumentsHandler.HandleListDocuments)
			r.Get("/documents/{docID}", adminDocumentsHandler.HandleGetDocument)
			r.Get("/documents/{docID}/content", adminDocumentsHandler.HandleDocumentContent)
			r.Get("/documents/{docID}/comments", adminDocumentsHandler.HandleListComments)
			r.Post("/documents/{docID}/comments", adminDocumentsHandler.HandleCreateComment)
			r.Get("/portal/documents", contentDocumentsHandler.HandlePortalList)
			r.Get("/portal/content-documents/{docID}", contentDocumentsHandler.HandlePortalGetContentDoc)
			r.Post("/feedback", feedbackHandler.HandleSubmit)
		})

		r.Route("/waiting-list", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))

			r.Post("/join", waitingListHandler.HandleJoinWaitingList)
			r.Get("/me", waitingListHandler.HandleGetMyPosition)
			r.Get("/portal", waitingListHandler.HandlePortalWaitingList)
			r.Put("/me/boat", waitingListHandler.HandleUpdateMyBoat)
			r.Post("/withdraw", waitingListHandler.HandleWithdraw)
			r.Post("/{entryID}/accept", waitingListHandler.HandleAcceptOffer)
			r.Post("/{entryID}/decline", waitingListHandler.HandleDeclineOffer)
		})

		if cfg.Features.Bookings {
			r.Route("/bookings", func(r chi.Router) {
				r.Get("/resources", bookingsHandler.HandleListResources)
				r.Get("/resources/{resourceID}/availability", bookingsHandler.HandleGetResourceAvailability)
				r.Get("/availability", bookingsHandler.HandleAggregateAvailability)
				r.Get("/availability/today", bookingsHandler.HandleTodayAvailability)
				r.Get("/hoist/slots", bookingsHandler.HandleHoistSlots)

				r.Group(func(r chi.Router) {
					r.Use(optionalSession)
					r.Post("/", bookingsHandler.HandleCreateBooking)
				})

				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthenticateSession(sessionService))
					r.Get("/me", bookingsHandler.HandleListMyBookings)
					r.Get("/{bookingID}", bookingsHandler.HandleGetBooking)
					r.Post("/{bookingID}/cancel", bookingsHandler.HandleCancelBooking)
				})

				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthenticateSession(sessionService))
					r.Use(middleware.RequireRole("board", "harbor_master"))
					r.Post("/{bookingID}/confirm", bookingsHandler.HandleConfirmBooking)
				})
			})
		}

		if cfg.Features.Calendar {
			r.Route("/calendar", func(r chi.Router) {
				r.Get("/", calendarHandler.HandleListPublicEvents)
				r.Get("/public.ics", calendarHandler.HandleExportICS)
				r.Get("/{eventID}", calendarHandler.HandleGetEvent)

				r.Group(func(r chi.Router) {
					r.Use(middleware.AuthenticateSession(sessionService))
					r.Use(middleware.RequireRole("board"))
					r.Post("/", calendarHandler.HandleCreateEvent)
					r.Put("/{eventID}", calendarHandler.HandleUpdateEvent)
					r.Delete("/{eventID}", calendarHandler.HandleDeleteEvent)
				})
			})
		}

		r.Route("/members", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/me", membersHandler.HandleGetMe)
			r.Put("/me", membersHandler.HandleUpdateMe)
			r.Get("/me/dashboard", membersHandler.HandleDashboard)
			r.Get("/me/boats", membersHandler.HandleListMyBoats)
			r.Post("/me/boats", membersHandler.HandleCreateBoat)
			r.Put("/me/boats/{boatID}", membersHandler.HandleUpdateBoat)
			r.Delete("/me/boats/{boatID}", membersHandler.HandleDeleteBoat)
			r.Get("/me/invoices", membersHandler.HandleListMyInvoices)
			r.Get("/me/invoices/updated-at", membersHandler.HandlePaymentDataUpdatedAt)
			r.Get("/me/invoices/{invoiceID}/pdf", invoiceHandler.HandleGetMyInvoicePDF)
			r.Get("/me/slip", membersHandler.HandleGetMySlip)
			r.Post("/me/slip/issues", membersHandler.HandleReportIssue)
			r.Get("/me/volunteer-hours", volunteerHandler.HandleGetMyVolunteerHours)
			r.Get("/me/data-export", gdprHandler.HandleDataExport)
			r.Post("/me/delete-request", gdprHandler.HandleRequestDeletion)
			r.Delete("/me/delete-request", gdprHandler.HandleCancelDeletion)
			r.Get("/me/delete-request", gdprHandler.HandleGetDeletionStatus)
			r.Post("/me/consent", gdprHandler.HandleRecordConsent)
			r.Get("/me/consents", gdprHandler.HandleGetMyConsents)
			r.Get("/me/email-preferences", emailPrefsHandler.HandleGetEmailPrefs)
			r.Put("/me/email-preferences", emailPrefsHandler.HandleUpdateEmailPref)
			r.Get("/directory", membersHandler.HandleGetDirectory)
		})

		r.Route("/slips", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/", placeholder("slips"))
		})

		r.Route("/portal/slip-shares", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/", slipSharesHandler.HandleListMySlipShares)
			r.Post("/", slipSharesHandler.HandleCreateSlipShare)
			r.Put("/{shareID}", slipSharesHandler.HandleUpdateSlipShare)
			r.Delete("/{shareID}", slipSharesHandler.HandleDeleteSlipShare)
			r.Get("/rebates", slipSharesHandler.HandleListMyRebates)
		})

		r.Route("/push", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/vapid-key", notificationsHandler.HandleGetVAPIDKey)
			r.Post("/subscribe", notificationsHandler.HandleSubscribe)
			r.Delete("/subscribe", notificationsHandler.HandleUnsubscribe)
		})

		r.Route("/members/me/notifications", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/", notificationsHandler.HandleGetPreferences)
			r.Put("/", notificationsHandler.HandleUpdatePreferences)
		})

		// Forum momentarily disabled — see BRY-191. Uncomment (and the
		// forumHandler instantiation above) to re-enable.
		// r.Route("/forum", func(r chi.Router) {
		// 	r.Use(middleware.AuthenticateSession(sessionService))
		// 	r.Get("/rooms", forumHandler.HandleListRooms)
		// 	r.Get("/rooms/{roomID}/messages", forumHandler.HandleGetRoomMessages)
		// 	r.Post("/rooms/{roomID}/messages", forumHandler.HandleSendMessage)
		// 	r.Get("/rooms/{roomID}/members", forumHandler.HandleGetRoomMembers)
		// })

		if cfg.Features.Projects {
			r.Route("/projects", func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))

				r.Get("/", projectsHandler.HandleListProjects)
				r.Get("/{projectID}", projectsHandler.HandleGetProject)
				r.Get("/{projectID}/tasks", projectsHandler.HandleListTasks)

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("board"))
					r.Post("/", projectsHandler.HandleCreateProject)
					r.Post("/{projectID}/tasks", projectsHandler.HandleCreateTask)
				})
			})

			r.Route("/tasks", func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))

				r.Post("/{taskID}/join", volunteerHandler.HandleJoinTask)
				r.Delete("/{taskID}/leave", volunteerHandler.HandleLeaveTask)
				r.Get("/{taskID}/participants", volunteerHandler.HandleListTaskParticipants)

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("board"))
					r.Put("/{taskID}", projectsHandler.HandleUpdateTask)
					r.Delete("/{taskID}", projectsHandler.HandleDeleteTask)
					r.Put("/{taskID}/assign", volunteerHandler.HandleAssignTask)
					r.Put("/{taskID}/hours", volunteerHandler.HandleAdjustHours)
				})
			})

			r.Route("/shopping-lists", func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))
				r.Get("/", shoppingListsHandler.HandleListShoppingLists)
				r.Post("/", shoppingListsHandler.HandleCreateShoppingList)
				r.Get("/{listID}", shoppingListsHandler.HandleGetShoppingList)
				r.Put("/{listID}", shoppingListsHandler.HandleUpdateShoppingList)
				r.Delete("/{listID}", shoppingListsHandler.HandleDeleteShoppingList)
				r.Get("/{listID}/items", shoppingListsHandler.HandleListItems)
				r.Post("/{listID}/items", shoppingListsHandler.HandleAddItem)
				r.Post("/{listID}/from-tasks", shoppingListsHandler.HandlePopulateFromTasks)
				r.Put("/items/{itemID}/toggle", shoppingListsHandler.HandleToggleItem)
				r.Delete("/items/{itemID}", shoppingListsHandler.HandleDeleteItem)
			})
		}

		r.Route("/feature-requests", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/", featureRequestsHandler.HandleListFeatureRequests)
			r.Post("/", featureRequestsHandler.HandleCreateFeatureRequest)
			r.Get("/{requestID}", featureRequestsHandler.HandleGetFeatureRequest)
			r.Post("/{requestID}/vote", featureRequestsHandler.HandleVote)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("board"))
				r.Put("/{requestID}/status", featureRequestsHandler.HandleUpdateFeatureRequestStatus)
				r.Post("/{requestID}/promote", featureRequestsHandler.HandlePromoteToTask)
			})
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))

			// /totp endpoints provision the TOTP factor itself — they
			// must NOT be gated by RequireAdminTOTP (chicken-and-egg).
			// /recover and /verify are how a user proves possession to
			// open the step-up window in the first place. /setup and
			// /confirm are the enrollment ceremony: open for a first
			// factor, but replacing an existing factor needs a fresh
			// step-up, or a stolen session could enroll its own secret.
			r.Route("/totp", func(r chi.Router) {
				r.Use(middleware.RequireAdminViewer())
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireFreshTOTPIfEnrolled())
					r.Post("/setup", totpHandler.HandleSetup)
					r.Post("/confirm", totpHandler.HandleConfirm)
				})
				r.Post("/verify", totpHandler.HandleVerify)
				r.Post("/recover", totpHandler.HandleRecover)

				// Code rotation requires a fresh TOTP — an attacker
				// with only a stale session cookie must not be able
				// to lock the legitimate owner out.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireFreshTOTPDefault())
					r.Post("/regenerate-codes", totpHandler.HandleRegenerateCodes)
				})
			})

			// Everything else under /admin requires a fresh TOTP
			// verification within the 12-hour step-up window.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdminTOTP(sessionService))

				r.Group(func(r chi.Router) {
					// The audit log carries actor IPs and security events, so it
					// stays with its original roles rather than every board role.
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/audit", auditHandler.HandleListAuditLog)
				})

				// DIL-365: read-only SQL query endpoint for operator
				// debugging. Admin-only + fresh TOTP. SQL must be
				// SELECT/WITH/EXPLAIN; runs under brygge_dev_ro role.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("admin"))
					r.Use(middleware.RequireFreshTOTPDefault())
					r.Post("/dev/query", devQueryHandler.HandleQuery)
				})
				// BRY-115: mint / revoke / list developer bearer tokens, gated on
				// the role a token's minter must keep (devtoken.MinterRole).
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole(devtoken.MinterRole))
					r.Use(middleware.RequireFreshTOTPDefault())
					r.Post("/dev/tokens", devTokenHandler.HandleCreate)
					r.Delete("/dev/tokens/{tokenID}", devTokenHandler.HandleRevoke)
				})
				r.With(middleware.RequireRole(devtoken.MinterRole)).Get("/dev/tokens", devTokenHandler.HandleList)

				if cfg.Features.Projects {
					r.Route("/volunteer", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("board"))
						r.Get("/hours", volunteerHandler.HandleListAllVolunteerHours)
						r.Put("/settings/hours", volunteerHandler.HandleSetRequiredHours)
						r.Post("/events/{eventID}/projects", volunteerHandler.HandleLinkProjectEvent)
						r.Delete("/events/{eventID}/projects/{projectID}", volunteerHandler.HandleUnlinkProjectEvent)
						r.Get("/events/{eventID}/projects", volunteerHandler.HandleGetEventProjects)
					})
				}

				r.Route("/map/markers", func(r chi.Router) {
					r.Use(middleware.RequireRole("board"))
					r.Post("/", mapHandler.HandleCreateMarker)
					r.Put("/{markerID}", mapHandler.HandleUpdateMarker)
					r.Delete("/{markerID}", mapHandler.HandleDeleteMarker)
				})

				r.Route("/boats", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board", "harbor_master"))
					r.Get("/unconfirmed", boatModelsHandler.HandleListUnconfirmed)
					r.Post("/{boatID}/confirm", boatModelsHandler.HandleConfirmBoat)
				})

				r.Route("/broadcasts", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board", "admin"))
					r.Get("/", broadcastsHandler.HandleList)
					r.Get("/{id}", broadcastsHandler.HandleGet)
					// Retry re-sends mail; gate on a fresh TOTP re-verify,
					// same posture as the inbox send path.
					r.With(middleware.RequireFreshTOTPDefault()).
						Post("/{id}/retry", broadcastsHandler.HandleRetry)
				})

				// One /financials block. Sub-paths are gated by the right
				// feature flag inline. Splitting into two r.Route calls
				// with the same prefix causes chi to register only one,
				// shadowing the other (this is what produced the 404 on
				// the invoice PDF endpoint).
				r.Route("/financials", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("treasurer", "board", "admin"))

					// Per-price-item aggregates over invoice_lines — independent
					// of the commerce module (which aggregates the Vipps
					// payments table). Available wherever faktura is enabled.
					r.Get("/price-item-summary", financialsHandler.HandleGetPriceItemSummary)
					r.Get("/reservations-by-month", financialsHandler.HandleGetReservationsByMonth)
					r.Get("/cash-flow", financialsHandler.HandleGetCashFlow)

					if cfg.Features.Commerce {
						// Commerce-side reporting: payments, overdue,
						// summary, CSV exports, and "generate from payment".
						r.Get("/summary", financialsHandler.HandleGetFinancialSummary)
						r.Get("/payments", financialsHandler.HandleListPayments)
						r.Get("/payments/{paymentID}", financialsHandler.HandleGetPaymentDetails)
						// A full payer list with names and emails: kept to its
						// original roles, like the member export.
						r.With(middleware.RequireRole("treasurer", "board", "admin")).
							Get("/export", financialsHandler.HandleExportCSV)
						r.Post("/invoices", financialsHandler.HandleGenerateInvoice)
						r.Get("/overdue", financialsHandler.HandleListOverdue)
					}

					if cfg.Features.Accounting {
						// Faktura lifecycle: PDF download, list (by status),
						// single create, bulk create, send, void, delete.
						// All gated on the accounting feature flag so the
						// whole faktura workflow can be toggled together.
						r.Get("/invoices/{invoiceID}/pdf", invoiceHandler.HandleGetInvoicePDF)
						r.Get("/invoices/{invoiceID}/pdf-archive", invoiceHandler.HandleListInvoicePDFArchive)
						r.Get("/invoices/{invoiceID}/pdf-archive/{archiveID}", invoiceHandler.HandleGetInvoicePDFArchiveBytes)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/full", invoiceHandler.HandleCreateInvoice)
						r.Get("/invoices/drafts", invoiceHandler.HandleListDraftInvoices)
						r.Get("/invoices/imported-count", invoiceHandler.HandleCountImportedInvoices)
						r.Get("/invoices", invoiceHandler.HandleListInvoices)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/bulk", invoiceHandler.HandleBulkCreateInvoices)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/send", invoiceHandler.HandleSendInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/resend", invoiceHandler.HandleResendInvoice)
						r.With(middleware.RequireRole("treasurer", "admin")).
							Get("/invoices/{invoiceID}/pay-link", invoiceHandler.HandleGetInvoicePayLink)
						// Durable per-member payables link (BRY-275): all of a member's
						// open fakturas on one no-login page. Same benign-read gating as
						// the per-invoice link above.
						r.With(middleware.RequireRole("treasurer", "admin")).
							Get("/users/{userID}/pay-link", invoiceHandler.HandleGetUserPayLink)
						// Per-invoice online-payment override (BRY-283): Auto/On/Off. A
						// reversible preference, role-gated but no fresh-TOTP step-up.
						r.With(middleware.RequireRole("treasurer", "admin")).
							Patch("/invoices/{invoiceID}/online-payment", invoiceHandler.HandleSetInvoiceOnlinePayment)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Get("/invoices/{invoiceID}/delivery-log", invoiceHandler.HandleGetDeliveryLog)
						// Removes a safety block SES set after a hard bounce, so it
						// takes a fresh step-up like the other faktura mail writes.
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/email-sends/{sendID}/unsuppress", invoiceHandler.HandleUnsuppressRecipient)
						// Statuses only (no addresses), for the list badge.
						r.With(middleware.RequireRole("treasurer", "admin")).
							Get("/invoices/delivery-statuses", invoiceHandler.HandleGetDeliveryStatuses)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/void", invoiceHandler.HandleVoidInvoice)
						// Credit-note links (BRY-342): settle a credit note against the
						// faktura it credits. Linking changes what members owe, so it
						// takes a fresh step-up; listing candidates is a read.
						r.With(middleware.RequireRole("treasurer", "admin")).
							Get("/invoices/{invoiceID}/credit-candidates", invoiceHandler.HandleListCreditCandidates)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/credit-link", invoiceHandler.HandleLinkCreditNote)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Delete("/invoices/{invoiceID}/credit-link", invoiceHandler.HandleUnlinkCreditNote)
						// "Ny faktura" after a card refund credited a faktura in
						// full (BRY-498): collects the money again, so it takes
						// the same step-up as creating a faktura.
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/{invoiceID}/reissue-after-refund", invoiceHandler.HandleReissueAfterRefund)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Delete("/invoices/{invoiceID}", invoiceHandler.HandleDeleteInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/bulk-reminder", invoiceHandler.HandleBulkSendReminder)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/bulk-regenerate-pdf", invoiceHandler.HandleBulkRegeneratePDF)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/bulk-reissue-kid", invoiceHandler.HandleBulkReissueKID)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/invoices/import/uni24", invoiceHandler.HandleImportUni24Invoices)
					}
				})

				if cfg.Features.Accounting {
					// The refund button (BRY-497). Starting a refund moves
					// money, so it is treasurer/admin with a fresh step-up
					// like the ledger's money gate; the order's refund
					// history is a read for the same roles.
					orderRefundHandler.Mount(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
				}

				r.Route("/users", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board", "admin"))
					r.Get("/", adminUsersHandler.HandleListUsers)
					r.Get("/email-bounces", adminUsersHandler.HandleListEmailBounces)
					r.Get("/{userID}", adminUsersHandler.HandleGetUser)
					r.Get("/{userID}/boats", adminUsersHandler.HandleListUserBoats)

					// CSV bulk import/export — site admin only. Export is a
					// read endpoint but emits PII for the entire club, so
					// it gates the same as import.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireRole("admin"))
						r.Get("/export.csv", adminUsersHandler.HandleExportUsersCSV)
						r.With(middleware.RequireFreshTOTPDefault()).
							Post("/import", adminUsersHandler.HandleImportUsersCSV)
						// Harbor share ownership import (BRY-139). Admin-only,
						// which is what lets it write admin members without
						// a per-row BRD-9 check.
						r.With(middleware.RequireFreshTOTPDefault()).
							Post("/harbor-shares/import", adminUsersHandler.HandleImportHarborShares)
					})

					// High-blast-radius mutations — re-prompt for TOTP
					// each time, regardless of the 12h step-up window.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireFreshTOTPDefault())
						r.Post("/", adminUsersHandler.HandleCreateUser)
						r.Patch("/{userID}", adminUsersHandler.HandleUpdateUser)
						r.Put("/{userID}/roles", adminUsersHandler.HandleUpdateUserRoles)
						r.Put("/{userID}/slips", adminUsersHandler.HandleSetUserSlips)
						r.Group(func(r chi.Router) {
							r.Use(middleware.RequireRole("admin"))
							r.Post("/{userID}/boats", adminUsersHandler.HandleCreateUserBoat)
							r.Put("/{userID}/boats/{boatID}", adminUsersHandler.HandleUpdateUserBoat)
							r.Delete("/{userID}/boats/{boatID}", adminUsersHandler.HandleDeleteUserBoat)
							r.Put("/{userID}/boats/{boatID}/slip", adminUsersHandler.HandleSetUserBoatSlip)
						})
						// Erasing a member is admin-only (BRD-9). Deactivate and
						// reactivate stay open to the board for ordinary members;
						// the handlers refuse an admin target for a non-admin.
						r.With(middleware.RequireRole("admin")).
							Delete("/{userID}", adminUsersHandler.HandleDeleteUser)
						r.Post("/{userID}/deactivate", adminUsersHandler.HandleDeactivateUser)
						r.Post("/{userID}/reactivate", adminUsersHandler.HandleReactivateUser)

						// Lost-device backstop — admin disables a target
						// user's TOTP so they can re-enroll. Acting admin
						// must be fresh-verified (no privilege loop).
						r.Group(func(r chi.Router) {
							r.Use(middleware.RequireRole("admin"))
							r.Post("/{userID}/totp/disable", totpHandler.HandleAdminDisableTOTP)
							// Same backstop for passkeys — wipe a locked-out
							// member's passkeys so they can re-enroll.
							r.Delete("/{userID}/passkeys", passkeyHandler.HandleAdminReset)
						})
					})
				})

				if inboxReconciler != nil && inboxReconciler.HasMailboxes() {
					// DIL-277 + DIL-321: per-user JMAP auth. Users
					// authenticate as themselves (creds from
					// user_mail_credentials, decrypted via the
					// provisioner); admin creds are used only for the
					// one-time Principal/get lookup that maps each
					// shared mailbox's address to its JMAP account id.
					inboxFactory := mail.NewJMAPFactory(cfg.StalwartAdminURL)
					inboxSpec, _ := mail.LoadSpec(cfg.BoardMailboxesPath)
					// PrincipalPasswords is reused from the reconciler's
					// load step above. Send (DIL-278) needs to auth as
					// the shared principal; reads (DIL-277) don't.
					inboxPasswords, _ := mail.LoadPasswordMap(cfg.StalwartMailboxPasswordsPath)
					inboxHandler := handlers.NewInboxHandler(
						db, inboxFactory, userProvisioner,
						cfg.StalwartAdminUser, cfg.StalwartAdminPassword,
						inboxPasswords, cfg.ClubSlug, cfg.ClubName, auditService, inboxSpec,
						cfg.FrontendURL, cfg.TOTPEncryptionKey, log,
					)
					// Bulk (group/BCC) sends are enqueued by the inbox
					// handler and drained by this worker, which sends each
					// recipient individually through the same principal
					// send path. The kick lets an enqueue start delivery
					// immediately instead of waiting for the next tick.
					// main creates the delivery worker before the routes and
					// runs it, with or without SMTP, so it is never nil here.
					deliveryWorker.RegisterSender(broadcast.KindBroadcast, broadcast.NewBroadcastSender(deliveryStore, inboxHandler))
					inboxHandler.SetBroadcastKick(deliveryWorker.Kick)
					r.Route("/inbox", func(r chi.Router) {
						// Read-only surface gated on having ANY board-mailbox
						// role; the per-address check (matching the spec's
						// `role`) lives inside each handler.
						r.Use(middleware.RequireRole(
							"chair", "vice_chair", "treasurer",
							"harbor_master", "secretary", "board", "admin",
						))
						r.Get("/mailboxes", inboxHandler.HandleListMailboxes)
						r.Get("/proxy-image", inboxHandler.HandleProxyImage)
						r.Get("/{address}/folders", inboxHandler.HandleListFolders)
						r.Route("/{address}/threads", func(r chi.Router) {
							r.Get("/", inboxHandler.HandleListThreads)
							r.Get("/{thread_id}", inboxHandler.HandleGetThread)
							r.Post("/{thread_id}/mark_read", inboxHandler.HandleMarkRead)
							r.Post("/{thread_id}/archive", inboxHandler.HandleArchiveThread)
						})
						r.Get("/{address}/blob/{blobId}", inboxHandler.HandleBlobDownload)
						// Outbound mail is irreversible; gate sends on a
						// fresh TOTP re-verify (10-min window), same
						// posture as void-invoice / delete-user.
						r.With(middleware.RequireFreshTOTPDefault()).
							Post("/{address}/send", inboxHandler.HandleSend)
						r.With(middleware.RequireFreshTOTPDefault()).
							Post("/{address}/blob", inboxHandler.HandleBlobUpload)
					})
				}

				// Queue management (BRY-477): list behind the step-up, offer /
				// enroll / reorder behind a fresh TOTP.
				waitingListHandler.MountAdminRoutes(r,
					middleware.RequireRoleOrBoardRead("board"),
					middleware.RequireFreshTOTPDefault(),
				)

				// The Payments page's filter groups (BRY-522): any admin viewer
				// reads them, the roles that run the page and own the price
				// list change them. Display configuration only, so no fresh
				// TOTP, like /admin/pricing (PAY-15); no feature flag, as the
				// Payments page has none. The SPA mirrors the roles as
				// WRITE_ROLES.paymentFilters.
				paymentFilterHandler.MountAdminRoutes(r,
					middleware.RequireRoleOrBoardRead(handlers.PaymentFilterWriteRoles...),
				)

				r.Route("/slips", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board", "harbor_master", "admin"))
					r.Get("/", adminSlipsHandler.HandleListSlips)
					r.Get("/{slipID}", adminSlipsHandler.HandleGetSlip)

					// Mutating operations re-prompt for TOTP within a 5-min
					// window, matching the admin-users UX so the SPA can
					// surface the in-context modal instead of failing silently.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireFreshTOTPDefault())
						r.Post("/", adminSlipsHandler.HandleCreateSlip)
						r.Put("/{slipID}", adminSlipsHandler.HandleUpdateSlip)
						r.Delete("/{slipID}", adminSlipsHandler.HandleDeleteSlip)
						r.Put("/{slipID}/assignment-type", adminSlipsHandler.HandleSetSlipAssignmentType)
						r.Post("/{slipID}/release", adminSlipsHandler.HandleReleaseSlip)
					})
				})

				if cfg.Features.Bookings {
					r.Route("/bookings", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("board", "harbor_master"))
						r.Get("/", bookingsHandler.HandleListBookingsAdmin)
					})

					r.Route("/settings/booking", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("board"))
						r.Get("/", clubSettingsHandler.HandleGetBookingSettings)
						r.Put("/", clubSettingsHandler.HandleUpdateBookingSettings)
					})
				}

				// General club settings (default language). Not
				// feature-gated — language applies platform-wide.
				r.Route("/settings/general", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/", clubSettingsHandler.HandleGetGeneralSettings)
					r.Put("/", clubSettingsHandler.HandleUpdateGeneralSettings)
				})

				// Session & security settings (idle window, absolute cap,
				// admin TOTP window). Gated on fresh TOTP proof.
				r.With(middleware.RequireRole("board", "admin")).
					Get("/settings/security", clubSettingsHandler.HandleGetSessionSettings)
				r.With(
					middleware.RequireRole("board", "admin"),
					middleware.RequireFreshTOTPDefault(),
				).Put("/settings/security", clubSettingsHandler.HandleUpdateSessionSettings)

				r.With(middleware.RequireRole("board", "admin")).
					Get("/settings/feedback", clubSettingsHandler.HandleGetFeedbackSettings)
				r.With(
					middleware.RequireRole("board", "admin"),
					middleware.RequireFreshTOTPDefault(),
				).Put("/settings/feedback", clubSettingsHandler.HandleUpdateFeedbackSettings)

				// Site-wide settings (identity, board emails, harbor &
				// motorhome content). Flat paths so chi matches with no
				// trailing slash. The same handler still serves every
				// field in the clubs row — module-specific views consume
				// only the subset they care about. See DIL-358.
				r.With(middleware.RequireRoleOrBoardRead("treasurer", "admin")).
					Get("/settings/site", clubSettingsHandler.HandleGetFinancialSettings)
				r.With(
					middleware.RequireRole("treasurer", "admin"),
					middleware.RequireFreshTOTPDefault(),
				).Patch("/settings/site", clubSettingsHandler.HandleUpdateFinancialSettings)

				// Economy-specific assets (faktura logo). Gated on the
				// accounting feature flag.
				if cfg.Features.Accounting {
					r.With(middleware.RequireRoleOrBoardRead("treasurer", "admin")).
						Get("/settings/economy/faktura-logo", clubSettingsHandler.HandleGetFakturaLogo)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTPDefault(),
					).Post("/settings/economy/faktura-logo", clubSettingsHandler.HandleUploadFakturaLogo)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTPDefault(),
					).Delete("/settings/economy/faktura-logo", clubSettingsHandler.HandleDeleteFakturaLogo)

					// Role-based price exemptions (BRY-351). Changing them
					// changes what members are billed, so the write sits
					// behind the same gate as other money settings.
					r.With(middleware.RequireRoleOrBoardRead("treasurer", "admin")).
						Get("/settings/economy/exemptions", invoiceHandler.HandleGetExemptions)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTPDefault(),
					).Put("/settings/economy/exemptions", invoiceHandler.HandlePutExemptions)

					// Multi-account bank registry (drift/høyrente/other).
					// Faktura PDFs read the row flagged is_default_for_invoices;
					// statement uploads will match against account_number.
					r.Route("/settings/bank-accounts", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("treasurer", "admin"))
						r.Get("/", bankAccountsHandler.HandleList)
						r.With(middleware.RequireFreshTOTPDefault()).Post("/", bankAccountsHandler.HandleCreate)
						r.With(middleware.RequireFreshTOTPDefault()).Put("/{accountID}", bankAccountsHandler.HandleUpdate)
						r.With(middleware.RequireFreshTOTPDefault()).Delete("/{accountID}", bankAccountsHandler.HandleArchive)
					})
				}

				// Site logo lives outside the Accounting feature gate
				// because the navbar relies on it on every public page.
				r.With(middleware.RequireRole("treasurer", "admin")).
					Get("/settings/site-logo", clubSettingsHandler.HandleGetSiteLogo)
				r.With(
					middleware.RequireRole("treasurer", "admin"),
					middleware.RequireFreshTOTPDefault(),
				).Post("/settings/site-logo", clubSettingsHandler.HandleUploadSiteLogo)
				r.With(
					middleware.RequireRole("treasurer", "admin"),
					middleware.RequireFreshTOTPDefault(),
				).Delete("/settings/site-logo", clubSettingsHandler.HandleDeleteSiteLogo)

				r.Route("/slip-shares", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board", "harbor_master"))
					r.Get("/", slipSharesHandler.HandleListAllSlipShares)
					r.Get("/rebates", slipSharesHandler.HandleListAllRebates)
					r.Put("/rebates/{rebateID}", slipSharesHandler.HandleUpdateRebateStatus)
				})

				if cfg.Features.Accounting {
					r.Route("/pricing", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("admin", "treasurer"))
						r.Get("/", priceItemsHandler.HandleListAdmin)
						r.Post("/", priceItemsHandler.HandleCreate)
						r.Put("/{itemID}", priceItemsHandler.HandleUpdate)
						r.Delete("/{itemID}", priceItemsHandler.HandleDelete)
					})
				}

				if cfg.Features.Commerce {
					r.Route("/products", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("board", "admin"))
						r.Get("/", productsHandler.HandleListAdmin)
						r.Post("/", productsHandler.HandleCreate)
						r.Put("/{productID}", productsHandler.HandleUpdate)
						r.Delete("/{productID}", productsHandler.HandleDelete)
						r.Post("/{productID}/variants", productsHandler.HandleCreateVariant)
						r.Delete("/variants/{variantID}", productsHandler.HandleDeleteVariant)
					})
				}

				r.Route("/documents", func(r chi.Router) {
					r.Use(middleware.RequireRole("board"))
					r.Post("/", adminDocumentsHandler.HandleUploadDocument)
					r.Delete("/{docID}", adminDocumentsHandler.HandleDeleteDocument)
					r.Post("/{docID}/summarize", aiDocumentsHandler.HandleSummarizeComments)
					r.Post("/{docID}/agenda", aiDocumentsHandler.HandleGenerateAgenda)
				})

				r.Route("/content-documents", func(r chi.Router) {
					r.Use(middleware.RequireRoleOrBoardRead("board"))
					r.Get("/", contentDocumentsHandler.HandleAdminList)
					r.Post("/", contentDocumentsHandler.HandleAdminCreate)
					r.Put("/{docID}", contentDocumentsHandler.HandleAdminUpdate)
					r.Delete("/{docID}", contentDocumentsHandler.HandleAdminDelete)
				})

				r.Route("/faqs", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/", faqsHandler.HandleAdminList)
					r.Post("/", faqsHandler.HandleAdminCreate)
					r.Put("/reorder", faqsHandler.HandleReorder)
					r.Put("/{faqID}", faqsHandler.HandleAdminUpdate)
					r.Delete("/{faqID}", faqsHandler.HandleAdminDelete)
				})

				r.Route("/notifications", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/config", notificationsHandler.HandleGetConfig)
					r.Put("/config", notificationsHandler.HandleUpdateConfig)
					r.Post("/test", notificationsHandler.HandleTestPush)
				})

				// GDPR is one function, held by the site admin by default
				// (BRD-3, BRY-481); the SPA page is admin-only, so the API is too.
				// Processing a deletion also needs a fresh TOTP (BRY-509).
				gdprHandler.MountAdminRoutes(r,
					middleware.RequireRole("admin"),
					middleware.RequireFreshTOTPDefault(),
				)

				if cfg.Features.Accounting {
					r.Route("/accounting", func(r chi.Router) {
						r.Use(middleware.RequireRoleOrBoardRead("treasurer", "board", "admin"))
						// Every route that can create or change a posted GL
						// entry (directly, or by auto-matching during an
						// import or sync) needs the treasurer/admin role and a
						// fresh step-up; every board role can read the books.
						moneyGate := []func(http.Handler) http.Handler{
							middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault(),
						}

						// Ledger mutations re-prompt for a fresh TOTP within
						// the per-action window — same posture as invoice
						// void/delete and the bank-row assign endpoints below.
						// Deleting a GL account, closing/reopening a period,
						// and creating/posting/voiding journal entries are all
						// high-blast-radius financial movements (DIL-245).
						r.Route("/accounts", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListAccounts)
							r.Post("/", accountingHandler.HandleCreateAccount)
							r.Put("/{accountID}", accountingHandler.HandleUpdateAccount)
							r.With(middleware.RequireFreshTOTPDefault()).
								Delete("/{accountID}", accountingHandler.HandleDeleteAccount)
							r.Post("/seed", accountingHandler.HandleSeedAccounts)
						})

						r.Route("/periods", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListPeriods)
							r.Post("/", accountingHandler.HandleCreatePeriod)
							r.With(middleware.RequireFreshTOTPDefault()).
								Post("/{periodID}/close", accountingHandler.HandleClosePeriod)
							r.With(middleware.RequireFreshTOTPDefault()).
								Post("/{periodID}/reopen", accountingHandler.HandleReopenPeriod)
							// Marking a year reference changes what gets booked
							// and what is carried forward, so it is a money
							// action, not the board-writable period group's.
							r.With(moneyGate...).
								Post("/{periodID}/reference", accountingHandler.HandleSetPeriodReference)
						})

						accountingHandler.MountLedgerRoutes(r, moneyGate...)

						// Per-charge Vipps reconciliation (BRY-227): one item
						// per payer in the Tildel queue instead of a single
						// batched payout bilag.
						r.Get("/vipps-charges/unmatched", accountingHandler.HandleListUnreconciledVippsCharges)
						r.Get("/vipps-charges/{rowID}/potential-invoices", accountingHandler.HandleVippsChargePotentialInvoices)
						r.Get("/vipps-charges/{rowID}/suggestions", accountingHandler.HandleVippsChargeSuggestions)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/vipps-charges/{rowID}/assign-invoice", accountingHandler.HandleAssignVippsChargeToInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/vipps-charges/{rowID}/assign-account", accountingHandler.HandleCreateVippsChargeBilag)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/vipps-charges/{rowID}/dismiss", accountingHandler.HandleDismissVippsCharge)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/vipps-charges/{rowID}/unassign", accountingHandler.HandleUnassignVippsCharge)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/clear-vipps-payout", accountingHandler.HandleClearVippsPayout)

						// BRY-11 sub 5: learned aliases & message rules
						// for aggressive Vipps auto-classification.
						r.Get("/vipps/queue", accountingHandler.HandleListVippsQueue)
						// BRY-4: pull the Vipps Report API settlement for a date
						// into the same review queue as the CSV export.
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/vipps/report-sync", accountingHandler.HandleVippsReportSync)
						// BRY-383: an alias routes a payer's money to a
						// member's faktura, so teaching or removing one is
						// gated like the assigns.
						r.Route("/vipps/customer-aliases", func(r chi.Router) {
							accountingHandler.MountCustomerAliasRoutes(r,
								middleware.RequireRole("treasurer", "admin"),
								middleware.RequireFreshTOTPDefault())
						})
						r.Route("/vipps/message-rules", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListMessageRules)
							r.Post("/", accountingHandler.HandleUpsertMessageRule)
							r.Delete("/{ruleID}", accountingHandler.HandleDeleteMessageRule)
						})

						// DIL-392 Tildel tab — per-row manual
						// reconciliation. Static "unmatched" paths
						// must register before the parameterised
						// {rowID} block so chi routes them right.
						r.Get("/bank-rows/unmatched", bankRowsHandler.HandleListUnmatched)
						r.Get("/bank-rows/unmatched/count", bankRowsHandler.HandleCountUnmatched)
						r.Get("/bank-rows/unmatched/count-by-year", bankRowsHandler.HandleCountUnmatchedByYear)
						// BRY-366: read-only projection of the bank
						// auto-assign rule, open to board readers.
						r.Get("/bank-rows/auto-assign/preview", bankRowsHandler.HandleAutoAssignPreview)
						r.Get("/bank-rows/{rowID}/suggestions", bankRowsHandler.HandleSuggestions)
						r.Get("/bank-rows/{rowID}/potential-invoices", bankRowsHandler.HandlePotentialInvoices)
						r.Get("/bank-rows/{rowID}/partners", bankRowsHandler.HandlePartners)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/assign-invoice-multi", bankRowsHandler.HandleAssignInvoiceMulti)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/assign-account-group", bankRowsHandler.HandleAssignAccountGroup)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/assign-invoice", bankRowsHandler.HandleAssignInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/assign-account", bankRowsHandler.HandleAssignAccount)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/dismiss", bankRowsHandler.HandleDismiss)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/undismiss", bankRowsHandler.HandleUndismiss)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/unassign", bankRowsHandler.HandleUnassign)
						r.Get("/bank-rows/pending-refunds", bankRowsHandler.HandleListPendingRefunds)
						r.Get("/bank-rows/pending-refunds/count", bankRowsHandler.HandleCountPendingRefunds)
						r.Get("/bank-rows/{rowID}/suggest-refund-outbound", bankRowsHandler.HandleSuggestRefundOutbound)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTPDefault()).
							Post("/bank-rows/{rowID}/pair-refund", bankRowsHandler.HandlePairRefund)

						r.Route("/rules", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListRules)
							r.Post("/", accountingHandler.HandleCreateRule)
							r.Put("/{ruleID}", accountingHandler.HandleUpdateRule)
							r.Delete("/{ruleID}", accountingHandler.HandleDeleteRule)
						})

						r.Route("/reports", func(r chi.Router) {
							r.Get("/income-statement", accountingHandler.HandleIncomeStatement)
							r.Get("/income-statement/pdf", accountingHandler.HandleIncomeStatementPDF)
							r.Get("/balance-sheet", accountingHandler.HandleBalanceSheet)
							r.Get("/balance-sheet/pdf", accountingHandler.HandleBalanceSheetPDF)
							r.Get("/trial-balance", accountingHandler.HandleTrialBalance)
							r.Get("/general-ledger", accountingHandler.HandleGeneralLedger)
							r.Get("/momskomp", accountingHandler.HandleMomskompensasjon)
							r.Get("/momskomp/pdf", accountingHandler.HandleMomskompensasjonPDF)
							r.Post("/momskomp", accountingHandler.HandleSaveMomskompReport)
							r.Put("/momskomp/{reportID}/status", accountingHandler.HandleUpdateMomskompStatus)
						})
					})
				}
			}) // close TOTP-gated group
		})
	})

	// Mount OpenAPI docs on a separate sub-router to avoid conflicts with chi handlers.
	// Enabled by default; set DISABLE_API_DOCS=true in production.
	if os.Getenv("DISABLE_API_DOCS") != "true" {
		docsRouter := chi.NewRouter()
		docsAPI := oa.NewAPI(docsRouter, oa.Config{DocsEnabled: true})
		oa.RegisterAllOperations(docsAPI)
		r.Mount("/api/docs", http.StripPrefix("/api/docs", docsRouter))
		log.Info().Msg("API docs available at /api/docs/docs")
	}

	// Typed short address printed under a harbor QR (BRY-436). Served here,
	// outside /api/v1 and ahead of the SPA fallback, so the redirect to
	// /qr/{token} works without JavaScript.
	r.With(standardRL).Get("/q/{code}", harborHandler.HandleShortCodeRedirect)
	// A typed address often gains a trailing slash; resolve it the same way.
	r.With(standardRL).Get("/q/{code}/", harborHandler.HandleShortCodeRedirect)

	return r
}
