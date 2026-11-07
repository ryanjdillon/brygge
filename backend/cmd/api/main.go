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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	backend "github.com/brygge-klubb/brygge"
	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/ai"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/devtoken"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/handlers"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/passkey"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/sesevents"
	"github.com/brygge-klubb/brygge/internal/storage"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/telemetry"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

func main() {
	cfg := config.Load()
	middleware.SetFreshTOTPWindow(cfg.FreshTOTPWindow)

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

	trustedProxies, err := middleware.ParseTrustedProxies(cfg.TrustedProxyCIDRs)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse TRUSTED_PROXY_CIDRS")
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

	// Dedicated read-only pool for /admin/dev/query. Authenticates as the
	// restricted brygge_dev_ro LOGIN role (DEV_QUERY_DATABASE_URL), kept
	// entirely separate from the brygge pool so the role is a real
	// privilege boundary (see handlers.DevQueryHandler). Nil when unset —
	// the endpoint then reports 503 instead of borrowing the brygge pool.
	var devQueryDB *pgxpool.Pool
	if cfg.DevQueryDatabaseURL != "" {
		devCfg, derr := pgxpool.ParseConfig(cfg.DevQueryDatabaseURL)
		if derr != nil {
			log.Fatal().Err(derr).Msg("failed to parse DEV_QUERY_DATABASE_URL")
		}
		devCfg.MaxConns = 4
		devCfg.MinConns = 0
		devQueryDB, derr = pgxpool.NewWithConfig(ctx, devCfg)
		if derr != nil {
			log.Fatal().Err(derr).Msg("failed to connect dev-query pool")
		}
		defer devQueryDB.Close()
		log.Info().Msg("dev-query read-only pool enabled (brygge_dev_ro)")
	} else {
		log.Info().Msg("dev-query endpoint disabled (DEV_QUERY_DATABASE_URL unset)")
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

	emailClient, err := newEmailSender(&cfg, email.NewSendLog(db, cfg.ClubSlug), log)
	if err != nil {
		log.Fatal().Err(err).Msg("email delivery misconfigured")
	}

	var claudeClient *ai.ClaudeClient
	if cfg.AnthropicAPIKey != "" {
		claudeClient = ai.NewClaudeClient(cfg.AnthropicAPIKey)
		log.Info().Msg("AI document processing enabled (Anthropic API key configured)")
	} else {
		log.Info().Msg("AI document processing disabled (no ANTHROPIC_API_KEY)")
	}

	s3Client, err := storage.NewClient(cfg.S3Endpoint, cfg.S3BucketDocs, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise S3 client")
	}
	if s3Client.IsConfigured() {
		log.Info().Str("bucket", cfg.S3BucketDocs).Msg("S3 object storage enabled")
	} else {
		log.Warn().Msg("S3 object storage not configured — document uploads disabled")
	}

	s3LegalClient, err := storage.NewClient(cfg.S3Endpoint, cfg.S3BucketLegal, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise legal S3 client")
	}
	if s3LegalClient.IsConfigured() {
		log.Info().Str("bucket", cfg.S3BucketLegal).Msg("legal S3 storage enabled (invoices)")
	} else {
		log.Warn().Msg("S3_BUCKET_LEGAL not configured — invoice PDFs stored in DB only")
	}

	auditService := audit.NewService(db, log)
	sessionService := auth.NewSessionService(db)

	// Stalwart admin client: the outbound-queue gauges need only the admin
	// URL and credentials, so they run whenever it is configured.
	var stalwartAdmin *mail.AdminClient
	if cfg.StalwartAdminURL != "" {
		stalwartAdmin = mail.NewAdminClient(
			cfg.StalwartAdminURL,
			cfg.StalwartAdminUser,
			cfg.StalwartAdminPassword,
			cfg.StalwartAdminToken,
			log,
		)
		// Without a credential every poll would 401; a single startup
		// warning beats a gauge that only ever reports "unknown".
		if cfg.StalwartAdminPassword == "" && cfg.StalwartAdminToken == "" {
			log.Warn().Msg("mail queue metrics disabled (STALWART_ADMIN_PASSWORD and STALWART_ADMIN_TOKEN unset)")
		} else {
			queueMonitor := mail.NewQueueMonitor(stalwartAdmin, log)
			if err := queueMonitor.RegisterMetrics(); err != nil {
				log.Warn().Err(err).Msg("failed to register mail queue metrics")
			} else {
				go queueMonitor.Run(ctx, time.Minute)
			}
		}
	}

	// Per-user Stalwart provisioning (DIL-321). Nil when admin creds
	// aren't configured. Decoded once so handlers share it.
	var userProvisioner *mail.UserProvisioner
	if stalwartAdmin != nil && cfg.TOTPEncryptionKey != "" {
		encKey, kerr := hex.DecodeString(cfg.TOTPEncryptionKey)
		switch {
		case kerr != nil || len(encKey) != 32:
			log.Warn().Err(kerr).Msg("TOTP_ENCRYPTION_KEY invalid; user mail provisioning disabled")
		default:
			userProvisioner = mail.NewUserProvisioner(db, stalwartAdmin, auditService, encKey, cfg.Domain, log)
		}
	}

	// Shared-inbox ACL reconciler (DIL-275/276). Nil when Stalwart
	// admin creds aren't configured — feature is fully optional.
	var inboxReconciler *mail.Reconciler
	// Shared delivery worker (BRY-164 broadcasts + BRY-11 invoice
	// reminders): one durable, throttled, retried queue drained by a
	// per-kind Sender registry. Created when SMTP and/or the inbox stack is
	// up; the invoice sender is registered when SMTP is configured, the
	// broadcast sender when the inbox/JMAP stack is up. Started below.
	deliveryStore := broadcast.NewStore(db)
	var deliveryWorker *broadcast.Worker
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
				inboxReconciler = mail.NewReconciler(db, adminJMAP, jmapFactory, passwords, auditService, spec, cfg.ClubSlug, cfg.ClubName, cfg.ReconcilerDryRun, log)
			}
		}
	}

	featuresHandler := handlers.NewFeaturesHandler(&cfg, db, log)
	healthHandler := handlers.NewHealthHandler(db, rdb)
	auditHandler := handlers.NewAuditHandler(db, auditService, &cfg, log)
	authHandler := handlers.NewAuthHandler(db, &cfg, log)
	magicLinkHandler := handlers.NewMagicLinkHandler(db, &cfg, emailClient, sessionService, log)
	if userProvisioner != nil {
		magicLinkHandler.MailProvisioner = userProvisioner
	}
	totpHandler := handlers.NewTOTPHandler(db, &cfg, sessionService, auditService, log)
	demoAuthHandler := handlers.NewDemoAuthHandler(db, &cfg, sessionService, log)

	webAuthn, err := passkey.New(&cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("initialize passkey relying party")
	}
	passkeyHandler := handlers.NewPasskeyHandler(webAuthn, passkey.NewStore(db), passkey.NewChallengeStore(rdb), sessionService, auditService, &cfg, log)
	waitingListHandler := handlers.NewWaitingListHandler(db, rdb, &cfg, log)
	paymentFilterHandler := handlers.NewPaymentFilterHandler(db, auditService, log)
	adminUsersHandler := handlers.NewAdminUsersHandler(db, &cfg, log)
	if inboxReconciler != nil && inboxReconciler.HasMailboxes() {
		adminUsersHandler.RoleChangeHook = inboxReconciler.OnRoleChanged
	}
	if userProvisioner != nil {
		adminUsersHandler.MailProvisioner = userProvisioner
	}
	adminSlipsHandler := handlers.NewAdminSlipsHandler(db, &cfg, log)
	adminDocumentsHandler := handlers.NewAdminDocumentsHandler(db, &cfg, s3Client, log)
	contentDocumentsHandler := handlers.NewContentDocumentsHandler(db, log)
	aiDocumentsHandler := handlers.NewAIDocumentsHandler(db, claudeClient, &cfg, log)
	// Forum momentarily disabled — see BRY-191. Handler retained in
	// internal/handlers/forum.go; re-instantiate here to re-enable.
	// forumHandler := handlers.NewForumHandler(db, &cfg, log)
	bookingsHandler := handlers.NewBookingsHandler(db, rdb, &cfg, log)
	calendarHandler := handlers.NewCalendarHandler(db, &cfg, log)
	membersHandler := handlers.NewMembersHandler(db, &cfg, log)
	weatherHandler := handlers.NewWeatherHandler(db, rdb, &cfg, log)
	contactHandler := handlers.NewContactHandler(db, &cfg, emailClient, log)
	// History/retry surface for bulk sends (BRY-165). The retry kick is a
	// lazy closure because broadcastWorker is assigned later, when the
	// inbox/mail stack is wired; by the time a retry fires at runtime it's set.
	broadcastsHandler := handlers.NewBroadcastsHandler(db, func() {
		if deliveryWorker != nil {
			deliveryWorker.Kick()
		}
	}, log)
	emailPrefsHandler := handlers.NewEmailPrefsHandler(db, &cfg, log)
	projectsHandler := handlers.NewProjectsHandler(db, &cfg, log)
	featureRequestsHandler := handlers.NewFeatureRequestsHandler(db, &cfg, log)
	financialsHandler := handlers.NewFinancialsHandler(db, &cfg, auditService, log)
	// Online payment providers — each constructed only when the instance has
	// its credentials (Vipps: MSN present; Stripe: secret key and webhook
	// secret). With none, invoice/webhook handlers skip online payment and
	// fall back to KID-only fakturas.
	var (
		vippsClient  *vipps.Client
		stripeClient *stripe.Client
		providerList []payments.Provider
	)
	if cfg.VippsMSN != "" {
		vippsClient = vipps.New(cfg.VippsBaseURL(), cfg.VippsClientID, cfg.VippsClientSecret,
			cfg.VippsSubscriptionKey, cfg.VippsMSN, log)
		providerList = append(providerList, vipps.NewPaymentProvider(vippsClient))
	}
	switch complete, partial := cfg.StripeCredentials(); {
	case complete:
		stripeClient = stripe.New(stripe.DefaultBaseURL, cfg.StripeSecretKey, log)
		providerList = append(providerList, stripe.NewPaymentProvider(stripeClient, cfg.ClubSlug))
	case partial:
		log.Warn().Msg("stripe: STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET must both be set; card payments are disabled")
	}
	var paymentProviders *payments.Registry
	if len(providerList) > 0 {
		paymentProviders = payments.NewRegistry(providerList...)
	}
	refundService := payments.NewRefundService(db, paymentProviders, log)
	orderRefundHandler := handlers.NewOrderRefundHandler(refundService, &cfg, auditService, log)
	invoiceHandler := handlers.NewInvoiceHandler(db, &cfg, emailClient, auditService, s3LegalClient, paymentProviders, log)
	// "Remove from suppression list" in the delivery log (BRY-590) needs the
	// SES region and the app's AWS key; without them the action answers 503.
	if cfg.SESRegion != "" {
		if awsCfg, err := sesAWSConfig(ctx, cfg.SESRegion); err != nil {
			log.Error().Err(err).Msg("SES suppression list unavailable: AWS config failed")
		} else {
			invoiceHandler.SetSuppressionList(sesevents.NewSuppressionList(sesv2.NewFromConfig(awsCfg)))
		}
	}
	// The refund routes exist only with accounting (a refund is booked by
	// its pass), so the lists show a refund button only then too.
	if cfg.Features.Accounting {
		invoiceHandler.SetRefunds(refundService)
	}
	// Invoice reminders reuse the shared delivery queue whenever SMTP is
	// configured — independent of the inbox/JMAP stack, which may be absent
	// (e.g. all of local dev). The worker always exists, because the Stripe
	// booking pass queues the documents of card refunds on it whether or
	// not mail is configured: their sender renders the PDF and emails only
	// with SMTP, so a club that enables mail later never sends old credit
	// notes. The reminder Sender needs SMTP; the broadcast Sender is added
	// later inside the inbox block when that stack is present.
	deliveryWorker = broadcast.NewWorker(deliveryStore, cfg.BulkSendThrottle, log)
	deliveryWorker.RegisterSender(broadcast.KindInvoiceRefundDocument, handlers.NewRefundDocumentSender(invoiceHandler))
	// Membership grants, withheld grants and join refunds queue their emails
	// whether or not mail is configured; without it the sender fails them
	// with a note saying so.
	membership.SetLogger(log)
	membershipSender := handlers.NewMembershipNoticeSender(db, &cfg, emailClient, log)
	for _, kind := range handlers.MembershipNoticeKinds {
		deliveryWorker.RegisterSender(kind, membershipSender)
	}
	membersHandler.SetDeliveryKick(deliveryWorker.Kick)
	if emailClient != nil {
		deliveryWorker.RegisterSender(broadcast.KindInvoiceReminder, handlers.NewInvoiceReminderSender(invoiceHandler))
		invoiceHandler.SetDeliveryQueue(deliveryStore, deliveryWorker.Kick)
	}
	accountingSvc := accounting.NewService(db, auditService, log)
	accountingSvc.SetDeliveryKick(deliveryWorker.Kick)
	if cfg.Features.Accounting {
		invoiceHandler.SetLedger(accountingSvc)
	}
	accountingHandler := handlers.NewAccountingHandler(accountingSvc, auditService, s3LegalClient, claudeClient, vippsClient, log)
	bankRowsHandler := handlers.NewBankRowsHandler(accountingSvc, auditService, log)
	devQueryHandler := handlers.NewDevQueryHandler(devQueryDB, auditService, log)
	devTokenService := devtoken.NewService(db)
	devTokenHandler := handlers.NewDevTokenHandler(devTokenService, auditService, cfg.DevQueryTokensEnabled, log)
	priceItemsHandler := handlers.NewPriceItemsHandler(db, &cfg, auditService, log)
	faqsHandler := handlers.NewFaqsHandler(db, &cfg, log)
	productsHandler := handlers.NewProductsHandler(db, &cfg, log)
	vippsWebhookHandler := handlers.NewVippsWebhookHandler(db, &cfg, vippsClient, log)
	stripeWebhookHandler := handlers.NewStripeWebhookHandler(db, &cfg, stripeClient, log)
	if err := payments.NewAuthorizedAttemptAge(db, log).RegisterMetrics(); err != nil {
		log.Warn().Err(err).Msg("failed to register payment attempt metrics")
	}
	if stripeClient != nil {
		accountingHandler.SetStripe(stripeClient)
	}
	if cfg.Features.Accounting {
		stripeWebhookHandler.SetLedger(accountingSvc)
	}
	if emailClient != nil {
		stripeWebhookHandler.SetMailer(emailClient)
	}
	boatModelsHandler := handlers.NewBoatModelsHandler(db, log)
	volunteerHandler := handlers.NewVolunteerHandler(db, &cfg, log)
	shoppingListsHandler := handlers.NewShoppingListsHandler(db, &cfg, log)
	mapHandler := handlers.NewMapHandler(db, &cfg, log)
	harborHandler := handlers.NewHarborHandler(db, &cfg, log)
	if cfg.Features.Accounting {
		harborHandler.SetRefunds(refundService)
	}
	clubSettingsHandler := handlers.NewClubSettingsHandler(db, &cfg, log)
	bankAccountsHandler := handlers.NewBankAccountsHandler(db, log)
	slipSharesHandler := handlers.NewSlipSharesHandler(db, &cfg, log)
	notificationsHandler := handlers.NewNotificationsHandler(db, &cfg, log)
	gdprHandler := handlers.NewGDPRHandler(db, &cfg, log)
	joinHandler := handlers.NewJoinHandler(db, rdb, &cfg, emailClient, sessionService, auditService, log)
	feedbackHandler := handlers.NewFeedbackHandler(db, log)

	r := newRouter(routeDeps{
		cfg:                     &cfg,
		log:                     log,
		db:                      db,
		rdb:                     rdb,
		trustedProxies:          trustedProxies,
		sessionService:          sessionService,
		auditService:            auditService,
		devTokenService:         devTokenService,
		deliveryStore:           deliveryStore,
		deliveryWorker:          deliveryWorker,
		inboxReconciler:         inboxReconciler,
		userProvisioner:         userProvisioner,
		accountingHandler:       accountingHandler,
		adminDocumentsHandler:   adminDocumentsHandler,
		adminSlipsHandler:       adminSlipsHandler,
		adminUsersHandler:       adminUsersHandler,
		aiDocumentsHandler:      aiDocumentsHandler,
		auditHandler:            auditHandler,
		authHandler:             authHandler,
		bankAccountsHandler:     bankAccountsHandler,
		bankRowsHandler:         bankRowsHandler,
		boatModelsHandler:       boatModelsHandler,
		bookingsHandler:         bookingsHandler,
		broadcastsHandler:       broadcastsHandler,
		calendarHandler:         calendarHandler,
		clubSettingsHandler:     clubSettingsHandler,
		contactHandler:          contactHandler,
		contentDocumentsHandler: contentDocumentsHandler,
		demoAuthHandler:         demoAuthHandler,
		devQueryHandler:         devQueryHandler,
		devTokenHandler:         devTokenHandler,
		emailPrefsHandler:       emailPrefsHandler,
		faqsHandler:             faqsHandler,
		featureRequestsHandler:  featureRequestsHandler,
		featuresHandler:         featuresHandler,
		feedbackHandler:         feedbackHandler,
		financialsHandler:       financialsHandler,
		gdprHandler:             gdprHandler,
		harborHandler:           harborHandler,
		healthHandler:           healthHandler,
		invoiceHandler:          invoiceHandler,
		joinHandler:             joinHandler,
		magicLinkHandler:        magicLinkHandler,
		mapHandler:              mapHandler,
		membersHandler:          membersHandler,
		notificationsHandler:    notificationsHandler,
		orderRefundHandler:      orderRefundHandler,
		passkeyHandler:          passkeyHandler,
		paymentFilterHandler:    paymentFilterHandler,
		priceItemsHandler:       priceItemsHandler,
		productsHandler:         productsHandler,
		projectsHandler:         projectsHandler,
		shoppingListsHandler:    shoppingListsHandler,
		slipSharesHandler:       slipSharesHandler,
		stripeWebhookHandler:    stripeWebhookHandler,
		totpHandler:             totpHandler,
		vippsWebhookHandler:     vippsWebhookHandler,
		volunteerHandler:        volunteerHandler,
		waitingListHandler:      waitingListHandler,
		weatherHandler:          weatherHandler,
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

	// Bring existing user principals' description/urls to the current
	// shape (display text, marker in urls). Idempotent, so every boot
	// runs it; once converged it is one admin GET per user.
	if userProvisioner != nil {
		go func() {
			bootCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			userProvisioner.ConvergePrincipalMetadata(bootCtx)
		}()
	}

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

	// Shared delivery worker (broadcasts + invoice reminders): drains the
	// queue on a background context (survives request lifecycles and
	// restarts). The boot sweep inside Run re-claims any deliveries orphaned
	// by a crash; the ticker is a safety net behind the per-enqueue kick.
	// Present whenever SMTP and/or the inbox stack is configured.
	if deliveryWorker != nil {
		go deliveryWorker.Run(ctx, time.Minute)
	}

	// Amazon SES delivery events (BRY-588): long-polls the configuration
	// set's SQS queue and records each event against its email_sends row.
	// AWS credentials come from the SDK's default chain (AWS_ACCESS_KEY_ID /
	// AWS_SECRET_ACCESS_KEY in the environment).
	sesStore := sesevents.NewStore(db)
	switch {
	case cfg.SESEventsQueueName == "":
		log.Info().Msg("SES events worker disabled (SES_EVENTS_QUEUE_NAME not set)")
	case cfg.SESRegion == "":
		log.Error().Msg("SES events worker not started: SES_EVENTS_QUEUE_NAME is set but SES_REGION is empty")
	default:
		awsCfg, err := sesAWSConfig(ctx, cfg.SESRegion)
		if err != nil {
			log.Error().Err(err).Msg("SES events worker not started: AWS config failed")
		} else {
			w := sesevents.NewWorker(sqs.NewFromConfig(awsCfg), sesStore,
				cfg.SESEventsQueueName, cfg.SESEventsDLQName, log)
			go w.Run(ctx)
		}
	}

	// Stalwart delivery outcomes (BRY-615): folds the mail journal into
	// email_sends so a Stalwart bounce reaches the faktura badge and the
	// member list. Needs the host's mail-log helper; legacy with it (BRY-594).
	if journalSweep := handlers.NewJournalSweep(db, &cfg, log); journalSweep != nil {
		go journalSweep.Run(ctx, handlers.JournalSweepInterval)
	} else {
		log.Info().Msg("journal sweep disabled (MAIL_LOG_SOCKET not set)")
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
				if n, err := devTokenService.PurgeExpired(ctx); err != nil {
					log.Warn().Err(err).Msg("developer token purge failed")
				} else if n > 0 {
					log.Info().Int64("rows", n).Msg("purged expired developer tokens")
				}
				// A magic link holds the address it was sent to; keep it a
				// day past expiry, used or not.
				if n, err := magicLinkHandler.PurgeExpired(ctx); err != nil {
					log.Warn().Err(err).Msg("magic link purge failed")
				} else if n > 0 {
					log.Info().Int64("rows", n).Msg("purged expired magic links")
				}
				// Join applications live 24 hours; the form data of one never
				// confirmed must not outlast that (BRY-629).
				if n, err := joinHandler.PurgeExpired(ctx); err != nil {
					log.Warn().Err(err).Msg("membership application purge failed")
				} else if n > 0 {
					log.Info().Int64("rows", n).Msg("purged expired membership applications")
				}
				// Settlement grants membership in its own transaction; this
				// heals a paid join whose grant was missed and queues the
				// board's notice of a refunded join payment (BRY-630).
				// A board notice that failed for want of an address is sent
				// once the club has one.
				res, err := membership.Sweep(ctx, db)
				for _, w := range res.Withheld {
					log.Warn().Str("source", w).Msg("join payment settled but membership withheld; the board was told")
				}
				if err != nil {
					log.Error().Err(err).Msg("membership sweep failed")
				}
				// A join left unpaid for 60 days ends: the applicant is
				// deactivated, with the join faktura credited when one was
				// issued, or the board is asked to review part-paid money
				// (BRY-631).
				exp, err := invoiceHandler.ExpireUnpaidJoins(ctx)
				if err != nil {
					log.Error().Err(err).Msg("join expiry failed")
				}
				if exp.Credited > 0 || exp.Deactivated > 0 || exp.Reviews > 0 {
					log.Info().Int("credited", exp.Credited).
						Int("deactivated", exp.Deactivated).Int("reviews", exp.Reviews).Msg("expired unpaid joins")
					deliveryWorker.Kick()
				}
				requeued, err := membershipSender.RequeueUnaddressed(ctx)
				if err != nil {
					log.Error().Err(err).Msg("membership notice requeue failed")
				}
				if res.Granted > 0 || res.RefundNotices > 0 || len(res.Withheld) > 0 || requeued > 0 {
					log.Info().Int("granted", res.Granted).Int("refund_notices", res.RefundNotices).
						Int64("requeued", requeued).Msg("membership sweep")
					deliveryWorker.Kick()
				}
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

	// Daily sweep that anonymizes the PII of deactivated members once their
	// financial records clear the bokføringsloven §13 retention window
	// (BRY-207). Retention advances only at year boundaries, so daily is
	// ample; runs once at startup so a long-idle deploy catches up promptly.
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if n, err := accountingSvc.PurgeExpiredMemberPII(ctx); err != nil {
				log.Warn().Err(err).Msg("member PII purge failed")
			} else if n > 0 {
				log.Info().Int64("members", n).Msg("purged PII of past-retention deactivated members")
			}
			// An erased member's legal-document consents are kept as proof
			// until their financial retention ends (BRD-18).
			if n, err := accountingSvc.PurgeRetentionClearedConsents(ctx); err != nil {
				log.Warn().Err(err).Msg("retained consent purge failed")
			} else if n > 0 {
				log.Info().Int64("rows", n).Msg("purged consents of past-retention erased members")
			}
			// Same window for external billing recipients (BRY-234): scrub a
			// dormant non-member payer's PII once past retention; organizations
			// are retained (legal persons, not personal data).
			if n, err := accountingSvc.PurgeExpiredRecipientPII(ctx); err != nil {
				log.Warn().Err(err).Msg("recipient PII purge failed")
			} else if n > 0 {
				log.Info().Int64("recipients", n).Msg("purged PII of past-retention billing recipients")
			}
			// A membership cancelled online ends on 31 December; from the
			// next day the member is deactivated and leaves the waiting
			// list (BRY-633). Idempotent, so the startup run is harmless.
			ended, err := handlers.EndCancelledMemberships(ctx, db, accountingSvc.SystemActor, time.Now())
			if err != nil {
				log.Error().Err(err).Msg("membership end job failed")
			}
			if ended.Ended > 0 || len(ended.Refused) > 0 {
				log.Info().Int("ended", ended.Ended).Int("waiting_list_withdrawn", ended.Withdrawn).
					Strs("refused_last_admin", ended.Refused).Msg("ended cancelled memberships")
			}
			if ended.Queued {
				deliveryWorker.Kick()
			}
			// Outgoing-mail history holds recipient addresses; kept 13
			// months whether or not the SES worker runs, since the sender
			// records Stalwart sends in the same table.
			if sends, events, err := sesStore.PurgeExpired(ctx); err != nil {
				log.Warn().Err(err).Msg("email send history purge failed")
			} else if sends > 0 || events > 0 {
				log.Info().Int64("sends", sends).Int64("events", events).Msg("purged past-retention email send history")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Nightly Vipps settlement sync (BRY-4): pull the last few days of Report
	// API funds+fees into the Vipps-kø as an automated import (NULL actor).
	// Idempotent (row_hash dedup), so re-pulling recent days is free and covers
	// reports that finalise late or over a weekend. Runs once at startup too.
	if vippsClient != nil {
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				var clubID string
				if err := db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, cfg.ClubSlug).Scan(&clubID); err != nil {
					log.Warn().Err(err).Msg("vipps report sync: resolve club failed")
				} else if ledgers, err := vippsClient.LedgersParsed(ctx); err != nil || len(ledgers) == 0 {
					log.Warn().Err(err).Msg("vipps report sync: resolve ledger failed")
				} else {
					ledgerID := ledgers[0].LedgerID
					for d := 1; d <= 4; d++ {
						date := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
						res, err := accountingSvc.SyncVippsReport(ctx, accounting.NewVippsReportSource(vippsClient), clubID, "", ledgerID, date)
						switch {
						case err != nil:
							log.Warn().Err(err).Str("date", date).Msg("vipps report sync failed")
						case res.TryLater:
							log.Debug().Str("date", date).Msg("vipps report not finalised yet")
						case res.Imported > 0:
							log.Info().Str("date", date).Int("imported", res.Imported).
								Int("skipped", res.SkippedDup).Msg("vipps report synced")
						}
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}

	// Nightly Stripe balance sync: mirror balance transactions and payouts
	// into the Stripe review queue. Ingest only; booking needs an acting
	// user. Idempotent, so the overlap with the previous run is free. Runs
	// once at startup too.
	if stripeClient != nil && cfg.Features.Accounting {
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				var clubID string
				if err := db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, cfg.ClubSlug).Scan(&clubID); err != nil {
					log.Warn().Err(err).Msg("stripe balance sync: resolve club failed")
				} else if res, err := accountingSvc.SyncStripeBalance(ctx, stripeClient, clubID); err != nil {
					log.Warn().Err(err).Msg("stripe balance sync failed")
				} else {
					log.Info().Int("fetched", res.Fetched).Int("inserted", res.Inserted).
						Int("payouts_seen", res.PayoutsSeen).Int("errors", len(res.Errors)).
						Msg("stripe balance synced")
					if len(res.Errors) > 0 {
						log.Warn().Strs("errors", res.Errors).Msg("stripe balance sync: some rows failed")
					}
				}
				// Refunds Stripe never confirmed are looked up once a day too;
				// the refund list does it for the order being read.
				if clubID != "" {
					if res, err := refundService.SweepRequested(ctx, clubID); err != nil {
						log.Warn().Err(err).Msg("stripe refund sweep failed")
					} else if res.Checked > 0 {
						log.Info().Int("checked", res.Checked).Int("settled", res.Settled).
							Int("failed", res.Failed).Int("errors", res.Errors).Msg("stripe refunds swept")
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}

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
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// A request whose final segment has an extension (e.g. an /assets/
		// chunk, favicon, source map) is a file request, not an SPA route.
		base := path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		isFileRequest := strings.Contains(base, ".")

		if _, err := fs.Stat(frontendFS, path); err != nil {
			// A missing file request is almost always a stale hashed chunk
			// from a previous deploy. Return a real 404 so the browser gets
			// a clean error instead of index.html served as text/html (which
			// fails strict MIME checking for module scripts). Only SPA
			// navigation routes fall through to index.html.
			if isFileRequest {
				http.NotFound(w, req)
				return
			}
			indexFile, _ := fs.ReadFile(frontendFS, "index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(indexFile)
			return
		}

		// index.html must always revalidate so clients pick up new asset
		// hashes immediately after a deploy; content-hashed assets are
		// immutable and safe to cache for a year.
		if path == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, req)
	})
}

// sesAWSConfig loads the AWS config the SES clients share: credentials from
// the SDK's default chain (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY in the
// environment). IMDS is off: the host is not on EC2, and a missing key should
// fail fast rather than probe a link-local metadata endpoint.
func sesAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region),
		awsconfig.WithEC2IMDSClientEnableState(imds.ClientDisabled))
}
