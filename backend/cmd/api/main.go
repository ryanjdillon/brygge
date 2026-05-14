package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	backend "github.com/brygge-klubb/brygge"
	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/ai"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/handlers"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/middleware"
	oa "github.com/brygge-klubb/brygge/internal/openapi"
	"github.com/brygge-klubb/brygge/internal/telemetry"
)

func main() {
	cfg := config.Load()

	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().
		Timestamp().
		Str("service", "brygge-api").
		Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	otelShutdown, err := telemetry.Setup(ctx, telemetry.Options{
		ServiceName:      "brygge-api",
		ServiceVersion:   "1.0.0",
		ClubSlug:         cfg.ClubSlug,
		ClubDomain:       cfg.Domain,
		TraceSampleRatio: 0.1,
	})
	if err != nil {
		log.Warn().Err(err).Msg("failed to initialize OpenTelemetry — metrics and tracing disabled")
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelShutdown(shutdownCtx); err != nil {
				log.Warn().Err(err).Msg("error shutting down OpenTelemetry")
			}
		}()
		log.Info().Msg("OpenTelemetry initialized")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse database URL")
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdleTime
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = cfg.DBStatementTimeout

	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Warn().Err(err).Msg("database ping failed on startup")
	} else {
		log.Info().
			Int32("max_conns", cfg.DBMaxConns).
			Int32("min_conns", cfg.DBMinConns).
			Msg("connected to database")
	}

	if err := telemetry.RegisterPoolMetrics(db); err != nil {
		log.Warn().Err(err).Msg("failed to register database pool metrics")
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse redis URL")
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Warn().Err(err).Msg("redis ping failed on startup")
	} else {
		log.Info().Msg("connected to redis")
	}

	var emailClient email.Sender
	if cfg.SMTPHost != "" {
		emailClient = email.NewSMTPClient(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPPassword, cfg.EmailFrom, cfg.EmailReplyTo)
		log.Info().Str("host", cfg.SMTPHost).Int("port", cfg.SMTPPort).Str("from", cfg.EmailFrom).Str("reply_to", cfg.EmailReplyTo).Msg("email delivery enabled (SMTP)")
	} else {
		log.Warn().Msg("email delivery disabled (SMTP_HOST unset)")
	}

	var claudeClient *ai.ClaudeClient
	if cfg.AnthropicAPIKey != "" {
		claudeClient = ai.NewClaudeClient(cfg.AnthropicAPIKey)
		log.Info().Msg("AI document processing enabled (Anthropic API key configured)")
	} else {
		log.Info().Msg("AI document processing disabled (no ANTHROPIC_API_KEY)")
	}

	auditService := audit.NewService(db, log)
	sessionService := auth.NewSessionService(db)

	// Per-user Stalwart provisioning (DIL-321). Nil when admin creds
	// aren't configured. Decoded once so handlers share it.
	var userProvisioner *mail.UserProvisioner
	if cfg.StalwartAdminURL != "" && cfg.TOTPEncryptionKey != "" {
		encKey, kerr := hex.DecodeString(cfg.TOTPEncryptionKey)
		switch {
		case kerr != nil || len(encKey) != 32:
			log.Warn().Err(kerr).Msg("TOTP_ENCRYPTION_KEY invalid; user mail provisioning disabled")
		default:
			adminClient := mail.NewAdminClient(
				cfg.StalwartAdminURL,
				cfg.StalwartAdminUser,
				cfg.StalwartAdminPassword,
				cfg.StalwartAdminToken,
				log,
			)
			userProvisioner = mail.NewUserProvisioner(db, adminClient, auditService, encKey, cfg.Domain, log)
		}
	}

	// Shared-inbox ACL reconciler (DIL-275/276). Nil when Stalwart
	// admin creds aren't configured — feature is fully optional.
	var inboxReconciler *mail.Reconciler
	if cfg.StalwartAdminURL != "" && cfg.BoardMailboxesPath != "" {
		spec, err := mail.LoadSpec(cfg.BoardMailboxesPath)
		if err != nil {
			log.Warn().Err(err).Str("path", cfg.BoardMailboxesPath).Msg("failed to load board-mailbox spec")
		} else if len(spec) > 0 {
			passwords, err := mail.LoadPasswordMap(cfg.StalwartMailboxPasswordsPath)
			switch {
			case err != nil:
				// Loud failure: an unreadable password file would
				// leave the reconciler thrashing every 5 minutes.
				// Disable the feature entirely until the file is
				// readable; the operator sees the error in journald
				// and re-deploys after fixing perms.
				log.Error().Err(err).Str("path", cfg.StalwartMailboxPasswordsPath).
					Msg("mailbox passwords unreadable — reconciler disabled")
			case len(passwords) == 0:
				log.Info().Str("path", cfg.StalwartMailboxPasswordsPath).
					Msg("mailbox passwords empty — reconciler disabled (waiting for stalwart-mailbox-config to populate)")
			default:
				jmapFactory := mail.NewJMAPFactory(cfg.StalwartAdminURL)
				adminJMAP := jmapFactory.AsPrincipal(cfg.StalwartAdminUser, cfg.StalwartAdminPassword)
				inboxReconciler = mail.NewReconciler(db, adminJMAP, jmapFactory, passwords, auditService, spec, cfg.ReconcilerDryRun, log)
			}
		}
	}

	featuresHandler := handlers.NewFeaturesHandler(&cfg)
	healthHandler := handlers.NewHealthHandler(db, rdb)
	auditHandler := handlers.NewAuditHandler(db, auditService, &cfg, log)
	authHandler := handlers.NewAuthHandler(db, &cfg, log)
	magicLinkHandler := handlers.NewMagicLinkHandler(db, &cfg, emailClient, sessionService, log)
	if userProvisioner != nil {
		magicLinkHandler.MailProvisioner = userProvisioner
	}
	totpHandler := handlers.NewTOTPHandler(db, &cfg, sessionService, auditService, log)
	demoAuthHandler := handlers.NewDemoAuthHandler(db, &cfg, sessionService, log)
	waitingListHandler := handlers.NewWaitingListHandler(db, rdb, &cfg, log)
	adminUsersHandler := handlers.NewAdminUsersHandler(db, &cfg, log)
	if inboxReconciler != nil && inboxReconciler.HasMailboxes() {
		adminUsersHandler.RoleChangeHook = inboxReconciler.OnRoleChanged
	}
	if userProvisioner != nil {
		adminUsersHandler.MailProvisioner = userProvisioner
	}
	adminSlipsHandler := handlers.NewAdminSlipsHandler(db, &cfg, log)
	adminDocumentsHandler := handlers.NewAdminDocumentsHandler(db, &cfg, log)
	aiDocumentsHandler := handlers.NewAIDocumentsHandler(db, claudeClient, &cfg, log)
	forumHandler := handlers.NewForumHandler(db, &cfg, log)
	bookingsHandler := handlers.NewBookingsHandler(db, rdb, &cfg, log)
	calendarHandler := handlers.NewCalendarHandler(db, &cfg, log)
	membersHandler := handlers.NewMembersHandler(db, &cfg, log)
	weatherHandler := handlers.NewWeatherHandler(db, rdb, &cfg, log)
	contactHandler := handlers.NewContactHandler(&cfg, log)
	broadcastHandler := handlers.NewBroadcastHandler(db, &cfg, emailClient, log)
	projectsHandler := handlers.NewProjectsHandler(db, &cfg, log)
	featureRequestsHandler := handlers.NewFeatureRequestsHandler(db, &cfg, log)
	financialsHandler := handlers.NewFinancialsHandler(db, &cfg, auditService, log)
	invoiceHandler := handlers.NewInvoiceHandler(db, &cfg, emailClient, auditService, log)
	accountingSvc := accounting.NewService(db, auditService, log)
	accountingHandler := handlers.NewAccountingHandler(accountingSvc, auditService, log)
	priceItemsHandler := handlers.NewPriceItemsHandler(db, &cfg, log)
	productsHandler := handlers.NewProductsHandler(db, &cfg, log)
	ordersHandler := handlers.NewOrdersHandler(db, &cfg, log)
	boatModelsHandler := handlers.NewBoatModelsHandler(db, log)
	volunteerHandler := handlers.NewVolunteerHandler(db, &cfg, log)
	shoppingListsHandler := handlers.NewShoppingListsHandler(db, &cfg, log)
	mapHandler := handlers.NewMapHandler(db, &cfg, log)
	harborHandler := handlers.NewHarborHandler(db, &cfg, log)
	clubSettingsHandler := handlers.NewClubSettingsHandler(db, &cfg, log)
	slipSharesHandler := handlers.NewSlipSharesHandler(db, &cfg, log)
	notificationsHandler := handlers.NewNotificationsHandler(db, &cfg, log)
	gdprHandler := handlers.NewGDPRHandler(db, &cfg, log)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
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
			r.Get("/weather", weatherHandler.HandleGetWeather)
			r.Post("/contact", contactHandler.HandleContactForm)
			r.Get("/club", func(w http.ResponseWriter, r *http.Request) {
				// Public club info — name/slug/domain come from the static
				// deploy config so the response is available even before any
				// admin has opened the settings page; contact channels and
				// the logo flag come from the DB (filled via /admin/settings/
				// financials in the admin UI) so they can be edited without
				// a redeploy.
				var (
					address                  string
					phone                    string
					vhf                      string
					lat                      *float64
					lon                      *float64
					website                  string
					chairman                 string
					viceChairman             string
					treasurer                string
					secretary                string
					harborMaster             string
					hasLogo                  bool
					harborApproach           string
					harborDepth              string
					harborVHF                string
					harborCTATitle           string
					harborCTADescription     string
					motorhomePower           string
					motorhomeFacilities      string
					motorhomeCheckin         string
					motorhomeRules           string
					motorhomeCTATitle        string
					motorhomeCTADescription  string
				)
				_ = db.QueryRow(r.Context(),
					`SELECT COALESCE(address, ''),
					        COALESCE(phone, ''),
					        COALESCE(vhf_channel, ''),
					        latitude, longitude,
					        COALESCE(website_url, ''),
					        COALESCE(chairman_email, ''),
					        COALESCE(vice_chairman_email, ''),
					        COALESCE(treasurer_email, ''),
					        COALESCE(secretary_email, ''),
					        COALESCE(harbor_master_email, ''),
					        (site_logo_data IS NOT NULL AND octet_length(site_logo_data) > 0),
					        COALESCE(harbor_approach, ''),
					        COALESCE(harbor_depth, ''),
					        COALESCE(harbor_vhf, ''),
					        COALESCE(harbor_cta_title, ''),
					        COALESCE(harbor_cta_description, ''),
					        COALESCE(motorhome_power, ''),
					        COALESCE(motorhome_facilities, ''),
					        COALESCE(motorhome_checkin, ''),
					        COALESCE(motorhome_rules, ''),
					        COALESCE(motorhome_cta_title, ''),
					        COALESCE(motorhome_cta_description, '')
					   FROM clubs WHERE slug = $1`,
					cfg.ClubSlug,
				).Scan(&address, &phone, &vhf, &lat, &lon,
					&website, &chairman, &viceChairman, &treasurer, &secretary, &harborMaster, &hasLogo,
					&harborApproach, &harborDepth, &harborVHF, &harborCTATitle, &harborCTADescription,
					&motorhomePower, &motorhomeFacilities, &motorhomeCheckin, &motorhomeRules,
					&motorhomeCTATitle, &motorhomeCTADescription)
				handlers.JSON(w, http.StatusOK, map[string]any{
					"name":                       cfg.ClubName,
					"slug":                       cfg.ClubSlug,
					"domain":                     cfg.Domain,
					"address":                    address,
					"phone":                      phone,
					"vhf_channel":                vhf,
					"latitude":                   lat,
					"longitude":                  lon,
					"website_url":                website,
					"chairman_email":             chairman,
					"vice_chairman_email":        viceChairman,
					"treasurer_email":            treasurer,
					"secretary_email":            secretary,
					"harbor_master_email":        harborMaster,
					"has_logo":                   hasLogo, // legacy alias for has_site_logo
					"has_site_logo":              hasLogo,
					"harbor_approach":            harborApproach,
					"harbor_depth":               harborDepth,
					"harbor_vhf":                 harborVHF,
					"harbor_cta_title":           harborCTATitle,
					"harbor_cta_description":     harborCTADescription,
					"motorhome_power":            motorhomePower,
					"motorhome_facilities":       motorhomeFacilities,
					"motorhome_checkin":          motorhomeCheckin,
					"motorhome_rules":            motorhomeRules,
					"motorhome_cta_title":        motorhomeCTATitle,
					"motorhome_cta_description":  motorhomeCTADescription,
				})
			})
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
			r.Use(middleware.OptionalSessionAuth(sessionService))
			r.Get("/harbor/layout", harborHandler.HandleGetLayout)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Use(middleware.RequireRole("board", "admin", "harbor_master"))
			r.Put("/harbor/layout", harborHandler.HandlePutLayout)
		})

		if cfg.Features.Commerce {
			r.Route("/orders", func(r chi.Router) {
				r.Post("/", ordersHandler.HandleCreateOrder)
				r.Get("/{orderID}", ordersHandler.HandleGetOrder)
				r.Post("/{orderID}/confirm", ordersHandler.HandleConfirmOrder)
			})
		}

		r.Group(func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))
			r.Get("/documents", adminDocumentsHandler.HandleListDocuments)
			r.Get("/documents/{docID}", adminDocumentsHandler.HandleGetDocument)
		})

		r.Route("/waiting-list", func(r chi.Router) {
			r.Use(middleware.AuthenticateSession(sessionService))

			r.Post("/join", waitingListHandler.HandleJoinWaitingList)
			r.Get("/me", waitingListHandler.HandleGetMyPosition)
			r.Get("/portal", waitingListHandler.HandlePortalWaitingList)
			r.Put("/me/boat", waitingListHandler.HandleUpdateMyBoat)
			r.Post("/withdraw", waitingListHandler.HandleWithdraw)
			r.Post("/{entryID}/accept", waitingListHandler.HandleAcceptOffer)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("board"))
				r.Get("/", waitingListHandler.HandleListWaitingList)
				r.Post("/{entryID}/offer", waitingListHandler.HandleOfferSlip)
				r.Put("/{entryID}/position", waitingListHandler.HandleReorderEntry)
			})
		})

		if cfg.Features.Bookings {
			r.Route("/bookings", func(r chi.Router) {
				r.Get("/resources", bookingsHandler.HandleListResources)
				r.Get("/resources/{resourceID}/availability", bookingsHandler.HandleGetResourceAvailability)
				r.Get("/availability", bookingsHandler.HandleAggregateAvailability)
				r.Get("/availability/today", bookingsHandler.HandleTodayAvailability)
				r.Get("/hoist/slots", bookingsHandler.HandleHoistSlots)

				r.Group(func(r chi.Router) {
					r.Use(middleware.OptionalSessionAuth(sessionService))
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
			r.Get("/me/slip", membersHandler.HandleGetMySlip)
			r.Post("/me/slip/issues", membersHandler.HandleReportIssue)
			r.Get("/me/volunteer-hours", volunteerHandler.HandleGetMyVolunteerHours)
			r.Get("/me/data-export", gdprHandler.HandleDataExport)
			r.Post("/me/delete-request", gdprHandler.HandleRequestDeletion)
			r.Delete("/me/delete-request", gdprHandler.HandleCancelDeletion)
			r.Get("/me/delete-request", gdprHandler.HandleGetDeletionStatus)
			r.Post("/me/consent", gdprHandler.HandleRecordConsent)
			r.Get("/me/consents", gdprHandler.HandleGetMyConsents)
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

		if cfg.Features.Communications {
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

			r.Route("/forum", func(r chi.Router) {
				r.Use(middleware.AuthenticateSession(sessionService))
				r.Get("/rooms", forumHandler.HandleListRooms)
				r.Get("/rooms/{roomID}/messages", forumHandler.HandleGetRoomMessages)
				r.Post("/rooms/{roomID}/messages", forumHandler.HandleSendMessage)
				r.Get("/rooms/{roomID}/members", forumHandler.HandleGetRoomMembers)
			})
		}

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
			// Role-gated only. /recover and /verify are how a user
			// proves possession to open the step-up window in the first
			// place; /setup and /confirm are the enrollment ceremony.
			r.Route("/totp", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin", "board", "treasurer"))
				r.Post("/setup", totpHandler.HandleSetup)
				r.Post("/confirm", totpHandler.HandleConfirm)
				r.Post("/verify", totpHandler.HandleVerify)
				r.Post("/recover", totpHandler.HandleRecover)

				// Code rotation requires a fresh TOTP — an attacker
				// with only a stale session cookie must not be able
				// to lock the legitimate owner out.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireFreshTOTP(10 * time.Minute))
					r.Post("/regenerate-codes", totpHandler.HandleRegenerateCodes)
				})
			})

			// Everything else under /admin requires a fresh TOTP
			// verification within the 12-hour step-up window.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdminTOTP(sessionService))

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/audit", auditHandler.HandleListAuditLog)
				})

				if cfg.Features.Projects {
					r.Route("/volunteer", func(r chi.Router) {
						r.Use(middleware.RequireRole("board"))
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
					r.Use(middleware.RequireRole("board", "harbor_master"))
					r.Get("/unconfirmed", boatModelsHandler.HandleListUnconfirmed)
					r.Post("/{boatID}/confirm", boatModelsHandler.HandleConfirmBoat)
				})

				if cfg.Features.Communications {
					r.Route("/broadcast", func(r chi.Router) {
						r.Use(middleware.RequireRole("board", "admin"))
						r.Post("/", broadcastHandler.HandleSendBroadcast)
					})

					r.Route("/broadcasts", func(r chi.Router) {
						r.Use(middleware.RequireRole("board", "admin"))
						r.Get("/", broadcastHandler.HandleListBroadcasts)
					})
				}

				// One /financials block. Sub-paths are gated by the right
				// feature flag inline. Splitting into two r.Route calls
				// with the same prefix causes chi to register only one,
				// shadowing the other (this is what produced the 404 on
				// the invoice PDF endpoint).
				r.Route("/financials", func(r chi.Router) {
					r.Use(middleware.RequireRole("treasurer", "board", "admin"))

					// Per-price-item aggregates over invoice_lines — independent
					// of the commerce module (which aggregates the Vipps
					// payments table). Available wherever faktura is enabled.
					r.Get("/price-item-summary", financialsHandler.HandleGetPriceItemSummary)

					if cfg.Features.Commerce {
						// Commerce-side reporting: payments, overdue,
						// summary, CSV exports, and "generate from payment".
						r.Get("/summary", financialsHandler.HandleGetFinancialSummary)
						r.Get("/payments", financialsHandler.HandleListPayments)
						r.Get("/payments/{paymentID}", financialsHandler.HandleGetPaymentDetails)
						r.Get("/export", financialsHandler.HandleExportCSV)
						r.Post("/invoices", financialsHandler.HandleGenerateInvoice)
						r.Get("/overdue", financialsHandler.HandleListOverdue)
					}

					if cfg.Features.Accounting {
						// Faktura lifecycle: PDF download, list (by status),
						// single create, bulk create, send, void, delete.
						// All gated on the accounting feature flag so the
						// whole faktura workflow can be toggled together.
						r.Get("/invoices/{invoiceID}/pdf", invoiceHandler.HandleGetInvoicePDF)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTP(10*time.Minute)).
							Post("/invoices/full", invoiceHandler.HandleCreateInvoice)
						r.Get("/invoices/drafts", invoiceHandler.HandleListDraftInvoices)
						r.Get("/invoices", invoiceHandler.HandleListInvoices)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTP(10*time.Minute)).
							Post("/invoices/bulk", invoiceHandler.HandleBulkCreateInvoices)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTP(10*time.Minute)).
							Post("/invoices/{invoiceID}/send", invoiceHandler.HandleSendInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTP(10*time.Minute)).
							Post("/invoices/{invoiceID}/void", invoiceHandler.HandleVoidInvoice)
						r.With(middleware.RequireRole("treasurer", "admin"),
							middleware.RequireFreshTOTP(10*time.Minute)).
							Delete("/invoices/{invoiceID}", invoiceHandler.HandleDeleteInvoice)
					}
				})

				r.Route("/users", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/", adminUsersHandler.HandleListUsers)
					r.Get("/{userID}", adminUsersHandler.HandleGetUser)
					r.Get("/{userID}/boats", adminUsersHandler.HandleListUserBoats)

					// CSV bulk import/export — site admin only. Export is a
					// read endpoint but emits PII for the entire club, so
					// it gates the same as import.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireRole("admin"))
						r.Get("/export.csv", adminUsersHandler.HandleExportUsersCSV)
						r.With(middleware.RequireFreshTOTP(10 * time.Minute)).
							Post("/import", adminUsersHandler.HandleImportUsersCSV)
					})

					// High-blast-radius mutations — re-prompt for TOTP
					// each time, regardless of the 12h step-up window.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireFreshTOTP(10 * time.Minute))
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
						r.Delete("/{userID}", adminUsersHandler.HandleDeleteUser)

						// Lost-device backstop — admin disables a target
						// user's TOTP so they can re-enroll. Acting admin
						// must be fresh-verified (no privilege loop).
						r.Group(func(r chi.Router) {
							r.Use(middleware.RequireRole("admin"))
							r.Post("/{userID}/totp/disable", totpHandler.HandleAdminDisableTOTP)
						})
					})
				})

				if inboxReconciler != nil && inboxReconciler.HasMailboxes() {
					adminClient := mail.NewAdminClient(
						cfg.StalwartAdminURL,
						cfg.StalwartAdminUser,
						cfg.StalwartAdminPassword,
						cfg.StalwartAdminToken,
						log,
					)
					// NOTE: DIL-277 read path is provisional. JMAP sessions
					// for admin only show admin's own account, so calls
					// scoped to a shared principal's accountId will not
					// work with these credentials. Tracked for the per-user
					// auth migration.
					jmapClient := mail.NewJMAPClient(cfg.StalwartAdminURL,
						cfg.StalwartAdminUser, cfg.StalwartAdminPassword, nil)
					spec, _ := mail.LoadSpec(cfg.BoardMailboxesPath)
					inboxHandler := handlers.NewInboxHandler(db, adminClient, jmapClient, auditService, spec, log)
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
						r.Route("/{address}/threads", func(r chi.Router) {
							r.Get("/", inboxHandler.HandleListThreads)
							r.Get("/{thread_id}", inboxHandler.HandleGetThread)
							r.Post("/{thread_id}/mark_read", inboxHandler.HandleMarkRead)
							r.Post("/{thread_id}/archive", inboxHandler.HandleArchiveThread)
						})
					})
				}

				r.Route("/slips", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "harbor_master", "admin"))
					r.Get("/", adminSlipsHandler.HandleListSlips)
					r.Get("/{slipID}", adminSlipsHandler.HandleGetSlip)

					// Mutating operations re-prompt for TOTP within a 5-min
					// window, matching the admin-users UX so the SPA can
					// surface the in-context modal instead of failing silently.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireFreshTOTP(10 * time.Minute))
						r.Post("/", adminSlipsHandler.HandleCreateSlip)
						r.Put("/{slipID}", adminSlipsHandler.HandleUpdateSlip)
						r.Delete("/{slipID}", adminSlipsHandler.HandleDeleteSlip)
						r.Post("/{slipID}/assign", adminSlipsHandler.HandleAssignSlip)
						r.Put("/{slipID}/assignment-type", adminSlipsHandler.HandleSetSlipAssignmentType)
						r.Post("/{slipID}/release", adminSlipsHandler.HandleReleaseSlip)
					})
				})

				if cfg.Features.Bookings {
					r.Route("/bookings", func(r chi.Router) {
						r.Use(middleware.RequireRole("board", "harbor_master"))
						r.Get("/", bookingsHandler.HandleListBookingsAdmin)
					})

					r.Route("/settings/booking", func(r chi.Router) {
						r.Use(middleware.RequireRole("board"))
						r.Get("/", clubSettingsHandler.HandleGetBookingSettings)
						r.Put("/", clubSettingsHandler.HandleUpdateBookingSettings)
					})
				}

				// Faktura branding settings (org no, address, bank, logo,
				// website, board emails). Gated on the accounting feature
				// flag alongside the rest of the faktura lifecycle. Flat
				// paths (not r.Route) so chi matches with no trailing slash.
				if cfg.Features.Accounting {
					r.With(middleware.RequireRole("treasurer", "admin")).
						Get("/settings/financials", clubSettingsHandler.HandleGetFinancialSettings)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTP(10*time.Minute),
					).Patch("/settings/financials", clubSettingsHandler.HandleUpdateFinancialSettings)
					r.With(middleware.RequireRole("treasurer", "admin")).
						Get("/settings/financials/faktura-logo", clubSettingsHandler.HandleGetFakturaLogo)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTP(10*time.Minute),
					).Post("/settings/financials/faktura-logo", clubSettingsHandler.HandleUploadFakturaLogo)
					r.With(
						middleware.RequireRole("treasurer", "admin"),
						middleware.RequireFreshTOTP(10*time.Minute),
					).Delete("/settings/financials/faktura-logo", clubSettingsHandler.HandleDeleteFakturaLogo)
				}

				// Site logo lives outside the Accounting feature gate
				// because the navbar relies on it on every public page.
				r.With(middleware.RequireRole("treasurer", "admin")).
					Get("/settings/site-logo", clubSettingsHandler.HandleGetSiteLogo)
				r.With(
					middleware.RequireRole("treasurer", "admin"),
					middleware.RequireFreshTOTP(10*time.Minute),
				).Post("/settings/site-logo", clubSettingsHandler.HandleUploadSiteLogo)
				r.With(
					middleware.RequireRole("treasurer", "admin"),
					middleware.RequireFreshTOTP(10*time.Minute),
				).Delete("/settings/site-logo", clubSettingsHandler.HandleDeleteSiteLogo)

				r.Route("/slip-shares", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "harbor_master"))
					r.Get("/", slipSharesHandler.HandleListAllSlipShares)
					r.Get("/rebates", slipSharesHandler.HandleListAllRebates)
					r.Put("/rebates/{rebateID}", slipSharesHandler.HandleUpdateRebateStatus)
				})

				if cfg.Features.Accounting {
					r.Route("/pricing", func(r chi.Router) {
						r.Use(middleware.RequireRole("admin", "treasurer"))
						r.Get("/", priceItemsHandler.HandleListAdmin)
						r.Post("/", priceItemsHandler.HandleCreate)
						r.Put("/{itemID}", priceItemsHandler.HandleUpdate)
						r.Delete("/{itemID}", priceItemsHandler.HandleDelete)
					})
				}

				if cfg.Features.Commerce {
					r.Route("/products", func(r chi.Router) {
						r.Use(middleware.RequireRole("board", "admin"))
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

				if cfg.Features.Communications {
					r.Route("/notifications", func(r chi.Router) {
						r.Use(middleware.RequireRole("board", "admin"))
						r.Get("/config", notificationsHandler.HandleGetConfig)
						r.Put("/config", notificationsHandler.HandleUpdateConfig)
						r.Post("/test", notificationsHandler.HandleTestPush)
					})
				}

				r.Route("/gdpr", func(r chi.Router) {
					r.Use(middleware.RequireRole("board", "admin"))
					r.Get("/deletion-requests", gdprHandler.HandleListDeletionRequests)
					r.Post("/deletion-requests/{requestID}/process", gdprHandler.HandleProcessDeletion)
					r.Get("/legal", gdprHandler.HandleAdminListLegalDocuments)
					r.Post("/legal", gdprHandler.HandleAdminCreateLegalDocument)
				})

				if cfg.Features.Accounting {
					r.Route("/accounting", func(r chi.Router) {
						r.Use(middleware.RequireRole("treasurer", "board", "admin"))

						r.Route("/accounts", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListAccounts)
							r.Post("/", accountingHandler.HandleCreateAccount)
							r.Put("/{accountID}", accountingHandler.HandleUpdateAccount)
							r.Delete("/{accountID}", accountingHandler.HandleDeleteAccount)
							r.Post("/seed", accountingHandler.HandleSeedAccounts)
						})

						r.Route("/periods", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListPeriods)
							r.Post("/", accountingHandler.HandleCreatePeriod)
							r.Post("/{periodID}/close", accountingHandler.HandleClosePeriod)
							r.Post("/{periodID}/reopen", accountingHandler.HandleReopenPeriod)
						})

						r.Route("/journal", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleListJournalEntries)
							r.Post("/", accountingHandler.HandleCreateJournalEntry)
							r.Get("/{entryID}", accountingHandler.HandleGetJournalEntry)
							r.Post("/{entryID}/post", accountingHandler.HandlePostJournalEntry)
							r.Post("/{entryID}/void", accountingHandler.HandleVoidJournalEntry)
						})

						r.Route("/sync", func(r chi.Router) {
							r.Post("/payments", accountingHandler.HandleSyncPayments)
							r.Post("/invoices", accountingHandler.HandleSyncInvoices)
							r.Post("/invoices/rebuild", accountingHandler.HandleRebuildInvoiceBilags)
						})

						r.Get("/bank-formats", accountingHandler.HandleListBankFormats)
						r.Route("/bank-import", func(r chi.Router) {
							r.Post("/", accountingHandler.HandleImportBankStatement)
							r.Get("/{importID}", accountingHandler.HandleGetBankImport)
							r.Get("/{importID}/unmatched", accountingHandler.HandleListUnmatchedRows)
							r.Post("/{importID}/rows/{rowID}/match", accountingHandler.HandleMatchBankRow)
							r.Post("/{importID}/auto-match", accountingHandler.HandleAutoMatchImport)
						})

						r.Route("/vipps-imports", func(r chi.Router) {
							r.Post("/", accountingHandler.HandleImportVippsSettlement)
							r.Get("/", accountingHandler.HandleListVippsImports)
							r.Get("/{importID}", accountingHandler.HandleGetVippsImport)
						})

						r.Get("/bank-imports", accountingHandler.HandleListBankImports)
						r.Get("/bank-rows", accountingHandler.HandleListBankRowsByAccount)
						r.Get("/vipps-rows", accountingHandler.HandleListVippsRowsByMSN)
						r.Post("/bank-sync", accountingHandler.HandleBankSync)

						r.Route("/bank-rows/{rowID}/reconcile-vipps", func(r chi.Router) {
							r.Get("/", accountingHandler.HandleVippsReconcilePreview)
							r.Post("/confirm", accountingHandler.HandleVippsReconcileConfirm)
						})

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

	frontendFS, err := backend.FrontendFS()
	if err != nil {
		log.Warn().Err(err).Msg("frontend assets not available")
	} else {
		serveFrontend(r, frontendFS)
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info().Str("addr", addr).Msg("starting server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// Shared-inbox ACL reconciler (DIL-275/276): one pass at boot to
	// recover from out-of-band drift, then every 5 min as the
	// self-healing safety net behind the role-change webhook.
	if inboxReconciler != nil && inboxReconciler.HasMailboxes() {
		go func() {
			bootCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			inboxReconciler.ReconcileAll(bootCtx)
		}()
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					tickCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
					inboxReconciler.ReconcileAll(tickCtx)
					cancel()
				}
			}
		}()
	}

	// Hourly background sweep of expired sessions. Cookies expire on the
	// client after 30 days; the row stays until something deletes it,
	// which would otherwise grow unbounded.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := sessionService.PurgeExpired(ctx)
				if err != nil {
					log.Warn().Err(err).Msg("session purge failed")
					continue
				}
				if n > 0 {
					log.Info().Int64("rows", n).Msg("purged expired sessions")
				}
			}
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}

	log.Info().Msg("server stopped")
}

func placeholder(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"module":%q,"status":"not_implemented"}`, name)
	}
}

func requestLogger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.Status()).
				Dur("latency", time.Since(start)).
				Msg("request")
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' blob:; style-src 'self' 'unsafe-inline'; "+
				"worker-src 'self' blob:; "+
				"img-src 'self' data: https:; "+
				"connect-src 'self' https://api.vipps.no https://apitest.vipps.no https://cache.kartverket.no https://tile.openstreetmap.org https://api.met.no; "+
				"font-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func serveFrontend(r chi.Router, frontendFS fs.FS) {
	fileServer := http.FileServer(http.FS(frontendFS))
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		// Try to serve the static file; fall back to index.html for SPA routes
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(frontendFS, path); err != nil {
			// File not found — serve index.html so the SPA router handles it
			indexFile, _ := fs.ReadFile(frontendFS, "index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexFile)
			return
		}
		fileServer.ServeHTTP(w, req)
	})
}
