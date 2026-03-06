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

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Warn().Err(err).Msg("database ping failed on startup")
	} else {
		log.Info().Msg("connected to database")
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

	healthHandler := handlers.NewHealthHandler(db, rdb)
	authHandler := handlers.NewAuthHandler(db, rdb, jwtService, vippsClient, &cfg, log)
	waitingListHandler := handlers.NewWaitingListHandler(db, rdb, &cfg, log)
	adminUsersHandler := handlers.NewAdminUsersHandler(db, &cfg, log)
	adminSlipsHandler := handlers.NewAdminSlipsHandler(db, &cfg, log)
	adminPricingHandler := handlers.NewAdminPricingHandler(db, &cfg, log)
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

	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/health", healthHandler)

		r.Route("/auth", func(r chi.Router) {
			r.Get("/vipps/login", authHandler.HandleVippsLogin)
			r.Get("/vipps/callback", authHandler.HandleVippsCallback)
			r.Post("/register", authHandler.HandleEmailRegister)
			r.Post("/login", authHandler.HandleEmailLogin)
			r.Post("/refresh", authHandler.HandleRefreshToken)

			r.Group(func(r chi.Router) {
				r.Use(middleware.Authenticate(jwtService))
				r.Post("/logout", authHandler.HandleLogout)
				r.Get("/me", authHandler.HandleMe)
			})
		})

		r.Get("/pricing", adminPricingHandler.HandleGetPricing)
		r.Get("/weather", weatherHandler.HandleGetWeather)
		r.Post("/contact", contactHandler.HandleContactForm)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/documents", adminDocumentsHandler.HandleListDocuments)
			r.Get("/documents/{docID}", adminDocumentsHandler.HandleGetDocument)
		})

		r.Route("/waiting-list", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))

			r.Post("/join", waitingListHandler.HandleJoinWaitingList)
			r.Get("/me", waitingListHandler.HandleGetMyPosition)
			r.Post("/withdraw", waitingListHandler.HandleWithdraw)
			r.Post("/{entryID}/accept", waitingListHandler.HandleAcceptOffer)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
				r.Get("/", waitingListHandler.HandleListWaitingList)
				r.Post("/{entryID}/offer", waitingListHandler.HandleOfferSlip)
				r.Put("/{entryID}/position", waitingListHandler.HandleReorderEntry)
			})
		})

		r.Route("/bookings", func(r chi.Router) {
			r.Get("/resources", bookingsHandler.HandleListResources)
			r.Get("/resources/{resourceID}/availability", bookingsHandler.HandleGetResourceAvailability)

			r.Group(func(r chi.Router) {
				r.Use(middleware.OptionalAuth(jwtService))
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

		r.Route("/members", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/me", membersHandler.HandleGetMe)
			r.Put("/me", membersHandler.HandleUpdateMe)
			r.Get("/me/boats", membersHandler.HandleListMyBoats)
			r.Post("/me/boats", membersHandler.HandleCreateBoat)
			r.Put("/me/boats/{boatID}", membersHandler.HandleUpdateBoat)
			r.Delete("/me/boats/{boatID}", membersHandler.HandleDeleteBoat)
			r.Get("/me/slip", membersHandler.HandleGetMySlip)
			r.Post("/me/slip/issues", membersHandler.HandleReportIssue)
			r.Get("/directory", membersHandler.HandleGetDirectory)
		})

		r.Route("/slips", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/", placeholder("slips"))
		})

		r.Route("/forum", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Get("/rooms", forumHandler.HandleListRooms)
			r.Get("/rooms/{roomID}/messages", forumHandler.HandleGetRoomMessages)
			r.Post("/rooms/{roomID}/messages", forumHandler.HandleSendMessage)
			r.Get("/rooms/{roomID}/members", forumHandler.HandleGetRoomMembers)
		})

		r.Route("/projects", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Use(middleware.RequireRole("styre"))
			r.Get("/", projectsHandler.HandleListProjects)
			r.Post("/", projectsHandler.HandleCreateProject)
			r.Get("/{projectID}", projectsHandler.HandleGetProject)
			r.Get("/{projectID}/tasks", projectsHandler.HandleListTasks)
			r.Post("/{projectID}/tasks", projectsHandler.HandleCreateTask)
		})

		r.Route("/tasks", func(r chi.Router) {
			r.Use(middleware.Authenticate(jwtService))
			r.Use(middleware.RequireRole("styre"))
			r.Put("/{taskID}", projectsHandler.HandleUpdateTask)
			r.Delete("/{taskID}", projectsHandler.HandleDeleteTask)
		})

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

			r.Route("/broadcast", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "admin"))
				r.Post("/", broadcastHandler.HandleSendBroadcast)
			})

			r.Route("/broadcasts", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "admin"))
				r.Get("/", broadcastHandler.HandleListBroadcasts)
			})

			r.Route("/financials", func(r chi.Router) {
				r.Use(middleware.RequireRole("treasurer", "styre", "admin"))
				r.Get("/summary", financialsHandler.HandleGetFinancialSummary)
				r.Get("/payments", financialsHandler.HandleListPayments)
				r.Get("/payments/{paymentID}", financialsHandler.HandleGetPaymentDetails)
				r.Get("/export", financialsHandler.HandleExportCSV)
				r.Post("/invoices", financialsHandler.HandleGenerateInvoice)
				r.Get("/overdue", financialsHandler.HandleListOverdue)
			})

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

			r.Route("/bookings", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre", "harbour_master"))
				r.Get("/", bookingsHandler.HandleListBookingsAdmin)
			})

			r.Route("/pricing", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin", "treasurer"))
				r.Put("/", adminPricingHandler.HandleUpdatePricing)
			})

			r.Route("/documents", func(r chi.Router) {
				r.Use(middleware.RequireRole("styre"))
				r.Post("/", adminDocumentsHandler.HandleUploadDocument)
				r.Delete("/{docID}", adminDocumentsHandler.HandleDeleteDocument)
				r.Post("/{docID}/summarize", aiDocumentsHandler.HandleSummarizeComments)
				r.Post("/{docID}/sakliste", aiDocumentsHandler.HandleGenerateSakliste)
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

func serveFrontend(r chi.Router, frontendFS fs.FS) {
	fileServer := http.FileServer(http.FS(frontendFS))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		fileServer.ServeHTTP(w, r)
	})
}
