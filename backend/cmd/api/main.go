package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	backend "github.com/brygge-klubb/brygge"
	"github.com/brygge-klubb/brygge/internal/ai"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/handlers"
	"github.com/brygge-klubb/brygge/internal/middleware"
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

	jwtService := auth.NewJWTService(&cfg)
	vippsClient := auth.NewVippsClient(&cfg)

	var claudeClient *ai.ClaudeClient
	if cfg.AnthropicAPIKey != "" {
		claudeClient = ai.NewClaudeClient(cfg.AnthropicAPIKey)
		log.Info().Msg("AI document processing enabled (Anthropic API key configured)")
	} else {
		log.Info().Msg("AI document processing disabled (no ANTHROPIC_API_KEY)")
	}

	auditService := audit.NewService(db, log)

	featuresHandler := handlers.NewFeaturesHandler(&cfg)
	healthHandler := handlers.NewHealthHandler(db, rdb)
	auditHandler := handlers.NewAuditHandler(db, auditService, &cfg, log)
	authHandler := handlers.NewAuthHandler(db, rdb, jwtService, vippsClient, &cfg, log, handlers.WithAuditService(auditService))
	waitingListHandler := handlers.NewWaitingListHandler(db, rdb, &cfg, log)
	adminUsersHandler := handlers.NewAdminUsersHandler(db, &cfg, log)
	adminSlipsHandler := handlers.NewAdminSlipsHandler(db, &cfg, log)
	adminDocumentsHandler := handlers.NewAdminDocumentsHandler(db, &cfg, log)
	aiDocumentsHandler := handlers.NewAIDocumentsHandler(db, claudeClient, &cfg, log)
	forumHandler := handlers.NewForumHandler(db, &cfg, log)
	bookingsHandler := handlers.NewBookingsHandler(db, rdb, &cfg, log)
	calendarHandler := handlers.NewCalendarHandler(db, &cfg, log)
	membersHandler := handlers.NewMembersHandler(db, &cfg, log)
	weatherHandler := handlers.NewWeatherHandler(db, rdb, &cfg, log)
	contactHandler := handlers.NewContactHandler(&cfg, log)
	broadcastHandler := handlers.NewBroadcastHandler(db, &cfg, log)
	projectsHandler := handlers.NewProjectsHandler(db, &cfg, log)
	featureRequestsHandler := handlers.NewFeatureRequestsHandler(db, &cfg, log)
	financialsHandler := handlers.NewFinancialsHandler(db, &cfg, log)
	priceItemsHandler := handlers.NewPriceItemsHandler(db, &cfg, log)
	productsHandler := handlers.NewProductsHandler(db, &cfg, log)
	ordersHandler := handlers.NewOrdersHandler(db, &cfg, log)
	boatModelsHandler := handlers.NewBoatModelsHandler(db, log)
	dugnadHandler := handlers.NewDugnadHandler(db, &cfg, log)
	shoppingListsHandler := handlers.NewShoppingListsHandler(db, &cfg, log)
	mapHandler := handlers.NewMapHandler(db, &cfg, log)
	clubSettingsHandler := handlers.NewClubSettingsHandler(db, &cfg, log)
	slipSharesHandler := handlers.NewSlipSharesHandler(db, &cfg, log)
	notificationsHandler := handlers.NewNotificationsHandler(db, &cfg, log)
	gdprHandler := handlers.NewGDPRHandler(db, &cfg, log)

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger(log))
	r.Use(chimw.Recoverer)
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
			r.Get("/vipps/status", authHandler.HandleVippsStatus)
			r.Get("/vipps/login", authHandler.HandleVippsLogin)
			r.Get("/vipps/callback", authHandler.HandleVippsCallback)

			r.Group(func(r chi.Router) {
				r.Use(strictRL)
				r.Post("/register", authHandler.HandleEmailRegister)
				r.Post("/login", authHandler.HandleEmailLogin)
				r.Post("/refresh", authHandler.HandleRefreshToken)
				r.Post("/exchange", authHandler.HandleAuthCodeExchange)
			})

			r.Group(func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
				r.Use(authedRL)
				r.Post("/logout", authHandler.HandleLogout)
				r.Get("/me", authHandler.HandleMe)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(standardRL)
			if cfg.Features.Commerce {
				r.Get("/pricing", priceItemsHandler.HandleListPublic)
				r.Get("/products", productsHandler.HandleListPublic)
			}
			r.Get("/boat-models", boatModelsHandler.HandleSearch)
			r.Get("/weather", weatherHandler.HandleGetWeather)
			r.Post("/contact", contactHandler.HandleContactForm)
		})

		r.Get("/legal/{docType}", gdprHandler.HandleGetLegalDocument)

		r.Route("/map", func(r chi.Router) {
			r.Get("/coordinates", mapHandler.HandleGetClubCoordinates)
			r.Get("/markers", mapHandler.HandleListMarkers)
			r.Get("/export/gpx", mapHandler.HandleExportGPX)
		})

		if cfg.Features.Commerce {
			r.Route("/orders", func(r chi.Router) {
				r.Post("/", ordersHandler.HandleCreateOrder)
				r.Get("/{orderID}", ordersHandler.HandleGetOrder)
				r.Post("/{orderID}/confirm", ordersHandler.HandleConfirmOrder)
			})
		}

		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/documents", adminDocumentsHandler.HandleListDocuments)
			r.Get("/documents/{docID}", adminDocumentsHandler.HandleGetDocument)
		})

		r.Route("/waiting-list", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))

			r.Post("/join", waitingListHandler.HandleJoinWaitingList)
			r.Get("/me", waitingListHandler.HandleGetMyPosition)
			r.Get("/portal", waitingListHandler.HandlePortalWaitingList)
			r.Put("/me/boat", waitingListHandler.HandleUpdateMyBoat)
			r.Post("/withdraw", waitingListHandler.HandleWithdraw)
			r.Post("/{entryID}/accept", waitingListHandler.HandleAcceptOffer)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
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
					r.Use(middleware.OptionalAuth(jwtService, middleware.WithLogger(log)))
					r.Post("/", bookingsHandler.HandleCreateBooking)
				})

				r.Group(func(r chi.Router) {
					r.Use(middleware.Authenticate(jwtService))
					r.Get("/me", bookingsHandler.HandleListMyBookings)
					r.Get("/{bookingID}", bookingsHandler.HandleGetBooking)
					r.Post("/{bookingID}/cancel", bookingsHandler.HandleCancelBooking)
				})

				r.Group(func(r chi.Router) {
					r.Use(middleware.Authenticate(jwtService))
					r.Use(middleware.RequireRole("styre", "harbour_master"))
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
					r.Use(middleware.Authenticate(jwtService))
					r.Use(middleware.RequireRole("styre"))
					r.Post("/", calendarHandler.HandleCreateEvent)
					r.Put("/{eventID}", calendarHandler.HandleUpdateEvent)
					r.Delete("/{eventID}", calendarHandler.HandleDeleteEvent)
				})
			})
		}

		r.Route("/members", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/me", membersHandler.HandleGetMe)
			r.Put("/me", membersHandler.HandleUpdateMe)
			r.Get("/me/dashboard", membersHandler.HandleDashboard)
			r.Get("/me/boats", membersHandler.HandleListMyBoats)
			r.Post("/me/boats", membersHandler.HandleCreateBoat)
			r.Put("/me/boats/{boatID}", membersHandler.HandleUpdateBoat)
			r.Delete("/me/boats/{boatID}", membersHandler.HandleDeleteBoat)
			r.Get("/me/slip", membersHandler.HandleGetMySlip)
			r.Post("/me/slip/issues", membersHandler.HandleReportIssue)
			r.Get("/me/dugnad-hours", dugnadHandler.HandleGetMyDugnadHours)
			r.Get("/me/data-export", gdprHandler.HandleDataExport)
			r.Post("/me/delete-request", gdprHandler.HandleRequestDeletion)
			r.Delete("/me/delete-request", gdprHandler.HandleCancelDeletion)
			r.Get("/me/delete-request", gdprHandler.HandleGetDeletionStatus)
			r.Post("/me/consent", gdprHandler.HandleRecordConsent)
			r.Get("/me/consents", gdprHandler.HandleGetMyConsents)
			r.Get("/directory", membersHandler.HandleGetDirectory)
		})

		r.Route("/slips", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/", placeholder("slips"))
		})

		r.Route("/portal/slip-shares", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/", slipSharesHandler.HandleListMySlipShares)
			r.Post("/", slipSharesHandler.HandleCreateSlipShare)
			r.Put("/{shareID}", slipSharesHandler.HandleUpdateSlipShare)
			r.Delete("/{shareID}", slipSharesHandler.HandleDeleteSlipShare)
			r.Get("/rebates", slipSharesHandler.HandleListMyRebates)
		})

		if cfg.Features.Communications {
			r.Route("/push", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
				r.Get("/vapid-key", notificationsHandler.HandleGetVAPIDKey)
				r.Post("/subscribe", notificationsHandler.HandleSubscribe)
				r.Delete("/subscribe", notificationsHandler.HandleUnsubscribe)
			})

			r.Route("/members/me/notifications", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
				r.Get("/", notificationsHandler.HandleGetPreferences)
				r.Put("/", notificationsHandler.HandleUpdatePreferences)
			})

			r.Route("/forum", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
				r.Get("/rooms", forumHandler.HandleListRooms)
				r.Get("/rooms/{roomID}/messages", forumHandler.HandleGetRoomMessages)
				r.Post("/rooms/{roomID}/messages", forumHandler.HandleSendMessage)
				r.Get("/rooms/{roomID}/members", forumHandler.HandleGetRoomMembers)
			})
		}

		if cfg.Features.Projects {
			r.Route("/projects", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))

				r.Get("/", projectsHandler.HandleListProjects)
				r.Get("/{projectID}", projectsHandler.HandleGetProject)
				r.Get("/{projectID}/tasks", projectsHandler.HandleListTasks)

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("styre"))
					r.Post("/", projectsHandler.HandleCreateProject)
					r.Post("/{projectID}/tasks", projectsHandler.HandleCreateTask)
				})
			})

			r.Route("/tasks", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))

				r.Post("/{taskID}/join", dugnadHandler.HandleJoinTask)
				r.Delete("/{taskID}/leave", dugnadHandler.HandleLeaveTask)
				r.Get("/{taskID}/participants", dugnadHandler.HandleListTaskParticipants)

				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("styre"))
					r.Put("/{taskID}", projectsHandler.HandleUpdateTask)
					r.Delete("/{taskID}", projectsHandler.HandleDeleteTask)
					r.Put("/{taskID}/assign", dugnadHandler.HandleAssignTask)
					r.Put("/{taskID}/hours", dugnadHandler.HandleAdjustHours)
				})
			})

			r.Route("/shopping-lists", func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
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
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/", featureRequestsHandler.HandleListFeatureRequests)
			r.Post("/", featureRequestsHandler.HandleCreateFeatureRequest)
			r.Get("/{requestID}", featureRequestsHandler.HandleGetFeatureRequest)
			r.Post("/{requestID}/vote", featureRequestsHandler.HandleVote)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
				r.Put("/{requestID}/status", featureRequestsHandler.HandleUpdateFeatureRequestStatus)
				r.Post("/{requestID}/promote", featureRequestsHandler.HandlePromoteToTask)
			})
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "admin"))
				r.Get("/audit", auditHandler.HandleListAuditLog)
			})

			if cfg.Features.Projects {
				r.Route("/dugnad", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre"))
					r.Get("/hours", dugnadHandler.HandleListAllDugnadHours)
					r.Put("/settings/hours", dugnadHandler.HandleSetRequiredHours)
					r.Post("/events/{eventID}/projects", dugnadHandler.HandleLinkProjectEvent)
					r.Delete("/events/{eventID}/projects/{projectID}", dugnadHandler.HandleUnlinkProjectEvent)
					r.Get("/events/{eventID}/projects", dugnadHandler.HandleGetEventProjects)
				})
			}

			r.Route("/map/markers", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
				r.Post("/", mapHandler.HandleCreateMarker)
				r.Put("/{markerID}", mapHandler.HandleUpdateMarker)
				r.Delete("/{markerID}", mapHandler.HandleDeleteMarker)
			})

			r.Route("/boats", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "harbour_master"))
				r.Get("/unconfirmed", boatModelsHandler.HandleListUnconfirmed)
				r.Post("/{boatID}/confirm", boatModelsHandler.HandleConfirmBoat)
			})

			if cfg.Features.Communications {
				r.Route("/broadcast", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre", "admin"))
					r.Post("/", broadcastHandler.HandleSendBroadcast)
				})

				r.Route("/broadcasts", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre", "admin"))
					r.Get("/", broadcastHandler.HandleListBroadcasts)
				})
			}

			if cfg.Features.Commerce {
				r.Route("/financials", func(r chi.Router) {
					r.Use(middleware.RequireRole("treasurer", "styre", "admin"))
					r.Get("/summary", financialsHandler.HandleGetFinancialSummary)
					r.Get("/payments", financialsHandler.HandleListPayments)
					r.Get("/payments/{paymentID}", financialsHandler.HandleGetPaymentDetails)
					r.Get("/export", financialsHandler.HandleExportCSV)
					r.Post("/invoices", financialsHandler.HandleGenerateInvoice)
					r.Get("/overdue", financialsHandler.HandleListOverdue)
				})
			}

			r.Route("/users", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "admin"))
				r.Get("/", adminUsersHandler.HandleListUsers)
				r.Get("/{userID}", adminUsersHandler.HandleGetUser)
				r.Put("/{userID}/roles", adminUsersHandler.HandleUpdateUserRoles)
				r.Delete("/{userID}", adminUsersHandler.HandleDeleteUser)
			})

			r.Route("/slips", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "harbour_master"))
				r.Get("/", adminSlipsHandler.HandleListSlips)
				r.Post("/", adminSlipsHandler.HandleCreateSlip)
				r.Get("/{slipID}", adminSlipsHandler.HandleGetSlip)
				r.Put("/{slipID}", adminSlipsHandler.HandleUpdateSlip)
				r.Post("/{slipID}/assign", adminSlipsHandler.HandleAssignSlip)
				r.Post("/{slipID}/release", adminSlipsHandler.HandleReleaseSlip)
			})

			if cfg.Features.Bookings {
				r.Route("/bookings", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre", "harbour_master"))
					r.Get("/", bookingsHandler.HandleListBookingsAdmin)
				})

				r.Route("/settings/booking", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre"))
					r.Get("/", clubSettingsHandler.HandleGetBookingSettings)
					r.Put("/", clubSettingsHandler.HandleUpdateBookingSettings)
				})
			}

			r.Route("/slip-shares", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "harbour_master"))
				r.Get("/", slipSharesHandler.HandleListAllSlipShares)
				r.Get("/rebates", slipSharesHandler.HandleListAllRebates)
				r.Put("/rebates/{rebateID}", slipSharesHandler.HandleUpdateRebateStatus)
			})

			if cfg.Features.Commerce {
				r.Route("/pricing", func(r chi.Router) {
					r.Use(middleware.RequireRole("admin", "treasurer"))
					r.Get("/", priceItemsHandler.HandleListAdmin)
					r.Post("/", priceItemsHandler.HandleCreate)
					r.Put("/{itemID}", priceItemsHandler.HandleUpdate)
					r.Delete("/{itemID}", priceItemsHandler.HandleDelete)
				})

				r.Route("/products", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre", "admin"))
					r.Get("/", productsHandler.HandleListAdmin)
					r.Post("/", productsHandler.HandleCreate)
					r.Put("/{productID}", productsHandler.HandleUpdate)
					r.Delete("/{productID}", productsHandler.HandleDelete)
					r.Post("/{productID}/variants", productsHandler.HandleCreateVariant)
					r.Delete("/variants/{variantID}", productsHandler.HandleDeleteVariant)
				})
			}

			r.Route("/documents", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
				r.Post("/", adminDocumentsHandler.HandleUploadDocument)
				r.Delete("/{docID}", adminDocumentsHandler.HandleDeleteDocument)
				r.Post("/{docID}/summarize", aiDocumentsHandler.HandleSummarizeComments)
				r.Post("/{docID}/sakliste", aiDocumentsHandler.HandleGenerateSakliste)
			})

			if cfg.Features.Communications {
				r.Route("/notifications", func(r chi.Router) {
					r.Use(middleware.RequireRole("styre", "admin"))
					r.Get("/config", notificationsHandler.HandleGetConfig)
					r.Put("/config", notificationsHandler.HandleUpdateConfig)
					r.Post("/test", notificationsHandler.HandleTestPush)
				})
			}

			r.Route("/gdpr", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "admin"))
				r.Get("/deletion-requests", gdprHandler.HandleListDeletionRequests)
				r.Post("/deletion-requests/{requestID}/process", gdprHandler.HandleProcessDeletion)
				r.Get("/legal", gdprHandler.HandleAdminListLegalDocuments)
				r.Post("/legal", gdprHandler.HandleAdminCreateLegalDocument)
			})
		})
	})

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
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https:; connect-src 'self' https://api.vipps.no https://apitest.vipps.no https://tiles.stadiamaps.com; "+
				"font-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func serveFrontend(r chi.Router, frontendFS fs.FS) {
	fileServer := http.FileServer(http.FS(frontendFS))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	})
}
