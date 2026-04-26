{
  config,
  lib,
  pkgs,
  clubConfig,
  ...
}:

let
  cfg = config.services.brygge;

  elementWeb = pkgs.element-web.override {
    conf = {
      default_server_config."m.homeserver" = {
        base_url = "https://matrix.${cfg.domain}";
        server_name = "matrix.${cfg.domain}";
      };
      default_server_name = "matrix.${cfg.domain}";
      brand = "Brygge";
      disable_guests = true;
      disable_3pid_login = true;
    };
  };
in
{
  imports = [
    ./disko.nix
  ];

  system.stateVersion = "25.05";
  nixpkgs.hostPlatform = "x86_64-linux";

  boot = {
    loader.grub = {
      enable = true;
      efiSupport = false;
    };
    kernelParams = [ "console=ttyS0,19200n8" ];
    tmp.cleanOnBoot = true;

    # Hetzner Cloud VMs are KVM/QEMU guests with virtio devices.
    # Without these the initrd cannot find /dev/sda and the kernel
    # hangs silently after the GRUB "booting the kernel" message.
    initrd.availableKernelModules = [
      "ahci"
      "xhci_pci"
      "virtio_pci"
      "virtio_scsi"
      "virtio_blk"
      "virtio_net"
      "sd_mod"
      "sr_mod"
    ];
    initrd.kernelModules = [
      "virtio_balloon"
      "virtio_console"
      "virtio_rng"
    ];
  };

  services.qemuGuest.enable = true;

  networking = {
    hostName = clubConfig.hostname;
    useDHCP = lib.mkDefault true;

    firewall = {
      enable = true;
      allowedTCPPorts = [
        22
        80
        443
        # Mail server (simple-nixos-mailserver)
        25   # SMTP (inbound from other MTAs)
        465  # SMTPS
        587  # Submission (STARTTLS)
        993  # IMAPS
      ];
      allowedUDPPorts = [
        443
      ];
    };
  };

  time.timeZone = clubConfig.timezone;

  users.users.root.openssh.authorizedKeys.keys = clubConfig.adminSshKeys;

  services.openssh = {
    enable = true;
    settings = {
      PermitRootLogin = "prohibit-password";
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
    };
  };

  security.sudo.wheelNeedsPassword = false;

  nix.settings = {
    experimental-features = [
      "nix-command"
      "flakes"
    ];
    trusted-users = [
      "root"
      "@wheel"
    ];
    auto-optimise-store = true;
  };

  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 14d";
  };

  environment.systemPackages = with pkgs; [
    git
    htop
    tmux
    vim
    curl
    jq
    postgresql_16
    redis
    go-migrate
    # Dendrite admin helpers (create-account, etc.) — needed by
    # `nix run .#gen-dendrite-token` and any manual recovery work.
    # NixOS doesn't put them on PATH automatically.
    dendrite
  ];

  services.postgresql = {
    enable = true;
    package = pkgs.postgresql_16;

    ensureDatabases = [
      "brygge"
      "dendrite"
    ];

    ensureUsers = [
      {
        name = "brygge";
        ensureDBOwnership = true;
      }
      {
        name = "dendrite";
        ensureDBOwnership = true;
      }
    ];

    authentication = lib.mkForce ''
      local all all              peer map=system-to-pg
      host  all all 127.0.0.1/32 scram-sha-256
      host  all all ::1/128      scram-sha-256
    '';

    identMap = ''
      system-to-pg root     postgres
      system-to-pg postgres postgres
      system-to-pg brygge   brygge
      system-to-pg dendrite dendrite
    '';

    settings = {
      max_connections = 100;
      shared_buffers = "256MB";
      effective_cache_size = "1GB";
    };
  };

  services.redis.servers.brygge = {
    enable = true;
    user = "brygge";
    unixSocket = "/run/redis-brygge/redis.sock";
    unixSocketPerm = 660;
    port = 0;
    settings = {
      maxmemory = "256mb";
      maxmemory-policy = "allkeys-lru";
    };
  };

  services.brygge = {
    enable = true;
    package = pkgs.brygge;
    domain = clubConfig.domain;
    environmentFile = "/etc/brygge/env";
    # Per-club feature flags from tfvars. Defaults live in nix/module.nix's
    # submodule (everything on except accounting + demoAuth); tfvars
    # carries only the keys this deployment overrides.
    features = clubConfig.features;
  };

  users.users.brygge.extraGroups = [ config.services.redis.servers.brygge.user ];

  services.dendrite = {
    enable = true;
    httpPort = 8008;
    # NixOS dendrite reads $REGISTRATION_SHARED_SECRET out of this file
    # at activation time and substitutes it into the rendered config.
    # Provisioned by `nix run .#gen-dendrite-token` on first run.
    # Without it, create-account fails with "Shared secret registration
    # is not enabled" because the placeholder substitutes to empty.
    environmentFile = "/etc/dendrite/env";
    settings = {
      global = {
        server_name = "matrix.${cfg.domain}";
        private_key = "/var/lib/dendrite/matrix_key.pem";
        trusted_third_party_id_servers = [
          "matrix.org"
          "vector.im"
        ];
        database = {
          connection_string = "postgres:///dendrite?host=/run/postgresql&sslmode=disable";
        };
      };
      client_api = {
        registration_disabled = true;
        registration_shared_secret = "$REGISTRATION_SHARED_SECRET";
      };
      logging = [
        {
          type = "std";
          level = "info";
        }
      ];
    };
  };

  systemd.services.dendrite = {
    after = [ "postgresql.service" "dendrite-secret-init.service" ];
    requires = [ "postgresql.service" "dendrite-secret-init.service" ];
    preStart = lib.mkAfter ''
      if [ ! -f /var/lib/dendrite/matrix_key.pem ]; then
        ${pkgs.dendrite}/bin/generate-keys -private-key /var/lib/dendrite/matrix_key.pem
      fi
    '';
  };

  # Bootstraps /etc/dendrite/env with a randomly-generated
  # REGISTRATION_SHARED_SECRET on first activation. Dendrite's
  # environmentFile setting REQUIRES this file to exist, so without
  # this oneshot the service can't start on a fresh deploy. Writes
  # the file once and leaves it alone on subsequent deploys (the
  # secret must persist; rotating it would invalidate any
  # outstanding registration tokens).
  systemd.services.dendrite-secret-init = {
    description = "Bootstrap dendrite registration shared secret";
    before = [ "dendrite.service" ];
    wantedBy = [ "dendrite.service" ];
    path = with pkgs; [ openssl coreutils ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
    };
    script = ''
      mkdir -p /etc/dendrite
      if [ ! -s /etc/dendrite/env ]; then
        SECRET=$(openssl rand -base64 48 | tr -d '\n')
        echo "REGISTRATION_SHARED_SECRET=$SECRET" > /etc/dendrite/env
        chmod 0400 /etc/dendrite/env
        chown dendrite:dendrite /etc/dendrite/env 2>/dev/null || true
        echo "provisioned /etc/dendrite/env with a fresh shared secret"
      fi
    '';
  };

  services.uptime-kuma = {
    enable = true;
    settings = {
      HOST = "127.0.0.1";
      PORT = "3001";
    };
  };

  services.caddy = {
    enable = true;
    email = clubConfig.adminEmail;

    virtualHosts."${cfg.domain}".extraConfig = ''
      encode gzip zstd
      reverse_proxy 127.0.0.1:${toString cfg.port}
    '';

    virtualHosts."matrix.${cfg.domain}".extraConfig = ''
      reverse_proxy 127.0.0.1:8008
    '';

    virtualHosts."element.${cfg.domain}".extraConfig = ''
      root * ${elementWeb}
      encode gzip zstd
      file_server
      try_files {path} /index.html
    '';

    virtualHosts."status.${cfg.domain}".extraConfig = ''
      reverse_proxy 127.0.0.1:3001
    '';

    # mail.<domain> serves Stalwart's HTTP listener (JMAP, admin, .well-known
    # autoconfig) behind TLS. security.acme (below) issues the cert via
    # HTTP-01 on webroot; Caddy serves the challenge alongside the proxy.
    # CORS: Bulwark (webmail.<domain>) calls JMAP cross-origin, so we need
    # to allow its origin and answer the OPTIONS preflight here.
    virtualHosts."mail.${cfg.domain}".extraConfig = ''
      tls /var/lib/acme/mail.${cfg.domain}/fullchain.pem /var/lib/acme/mail.${cfg.domain}/key.pem
      handle /.well-known/acme-challenge/* {
        root * /var/lib/acme/acme-challenge
        file_server
      }

      @preflight method OPTIONS
      handle @preflight {
        header Access-Control-Allow-Origin "https://webmail.${cfg.domain}"
        header Access-Control-Allow-Methods "GET, POST, PATCH, PUT, DELETE, OPTIONS"
        header Access-Control-Allow-Headers "Authorization, Content-Type, X-Requested-With, Origin, Accept"
        header Access-Control-Allow-Credentials "true"
        header Access-Control-Max-Age "86400"
        respond 204
      }

      header Access-Control-Allow-Origin "https://webmail.${cfg.domain}"
      header Access-Control-Allow-Credentials "true"
      header Vary "Origin"

      reverse_proxy 127.0.0.1:8088
    '';

    # Bulwark webmail (JMAP client, Next.js in a container).
    virtualHosts."webmail.${cfg.domain}".extraConfig = ''
      encode gzip zstd
      reverse_proxy 127.0.0.1:3000
    '';
  };

  # ACME cert for mail.<domain> — HTTP-01 challenge via webroot served by
  # Caddy (above). simple-nixos-mailserver reads it via useACMEHost.
  security.acme = {
    acceptTerms = true;
    defaults.email = clubConfig.adminEmail;
    certs."mail.${cfg.domain}" = {
      webroot = "/var/lib/acme/acme-challenge";
      group = "acme";
    };
  };

  # Caddy needs read access to the ACME webroot (0750 acme:acme) to serve
  # the challenge tokens. Without this the cert order gets 403.
  users.users.caddy.extraGroups = [ "acme" ];

  # Stalwart Mail Server — all-in-one SMTP + IMAP + JMAP (Rust).
  # Replaces simple-nixos-mailserver. JMAP is what Bulwark speaks.
  services.stalwart = {
    enable = true;
    stateVersion = "26.05";
    openFirewall = false; # firewall ports managed above in networking.firewall

    # Bootstrap admin — read from a root-owned file outside /nix/store so
    # the password doesn't end up in the world-readable store. systemd
    # LoadCredential exposes it at /run/credentials/stalwart.service/.
    # Create the file on the server with: install -m 0400 -o root /dev/stdin /etc/stalwart/admin-password <<<"yourpassword"
    credentials.admin_password = "/etc/stalwart/admin-password";

    settings = {
      server.hostname = "mail.${cfg.domain}";

      server.tls = {
        certificate = "default";
        enable = true;
        implicit = false;
      };
      certificate."default" = {
        # security.acme (below) writes here via HTTP-01 on webroot.
        cert = "%{file:/var/lib/acme/mail.${cfg.domain}/fullchain.pem}%";
        private-key = "%{file:/var/lib/acme/mail.${cfg.domain}/key.pem}%";
        default = true;
      };

      server.listener = {
        smtp = {
          bind = [ "[::]:25" ];
          protocol = "smtp";
        };
        submissions = {
          bind = [ "[::]:465" ];
          protocol = "smtp";
          tls.implicit = true;
        };
        submission = {
          bind = [ "[::]:587" ];
          protocol = "smtp";
        };
        imaptls = {
          bind = [ "[::]:993" ];
          protocol = "imap";
          tls.implicit = true;
        };
        # HTTP (JMAP + admin + autoconfig). Caddy terminates TLS externally.
        # Port 8088 to avoid collision with brygge's API on 127.0.0.1:8080.
        http = {
          bind = [ "127.0.0.1:8088" ];
          protocol = "http";
          tls.implicit = false;
          url = "https://mail.${cfg.domain}";
        };
      };

      # All-local storage on rocksdb.
      storage = {
        data = "rocksdb";
        fts = "rocksdb";
        blob = "rocksdb";
        lookup = "rocksdb";
        directory = "internal";
      };
      store.rocksdb = {
        type = "rocksdb";
        path = "/var/lib/stalwart/data";
        compression = "lz4";
      };
      directory.internal = {
        type = "internal";
        store = "rocksdb";
      };

      session.auth = {
        mechanisms = "[plain login]";
        directory = "'internal'";
      };
      session.rcpt.directory = "'internal'";
      # Default outbound routing: local delivery for our own domain, MX
      # lookup for everything else. Do NOT set queue.strategy.route to a
      # single literal — that forces ALL mail (including outbound to
      # gmail.com etc.) to route locally, which fails with
      # "550 Mailbox not found".

      # Fallback admin — used only until a real admin account is created
      # in the admin UI. Change via the UI immediately after first login.
      authentication.fallback-admin = {
        user = "admin";
        secret = "%{file:/run/credentials/stalwart.service/admin_password}%";
      };

      # DKIM — Stalwart auto-generates a key on first boot. Publish the
      # public key via tfvars.dkim_public_value after deploy (same flow as
      # before).
      signature."rsa-mail" = {
        private-key = "%{file:/var/lib/stalwart/dkim/mail-private.pem}%";
        domain = cfg.domain;
        selector = "mail";
        canonicalization = "relaxed/relaxed";
        algorithm = "rsa-sha256";
        set-body-length = false;
      };
    };
  };

  # Stalwart needs group access to /var/lib/acme/mail.<domain>/ to read
  # the cert security.acme issues (defaults are 0750 acme:acme).
  users.users.stalwart.extraGroups = [ "acme" ];

  # Provision a dedicated `relay@<domain>` principal in Stalwart for the
  # brygge API to authenticate against when sending magic-link / invoice /
  # broadcast mail. Keeping this separate from any human mailbox (e.g.
  # info@) means brygge's credentials are unaffected by webmail-side
  # password changes or mailbox renames.
  #
  # Plaintext lives in /etc/stalwart/relay-password (root:root 0400, not
  # in /nix/store). The same plaintext goes in /etc/brygge/env as
  # SMTP_PASSWORD — the two files are the source of truth for the
  # relay credentials and must be kept in sync when rotating.
  systemd.services.stalwart-relay-account = {
    description = "Provision the brygge SMTP relay@ principal in Stalwart";
    after = [ "stalwart.service" ];
    wants = [ "stalwart.service" ];
    wantedBy = [ "multi-user.target" ];
    path = with pkgs; [ curl jq mkpasswd coreutils ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
    };
    script = ''
      set -eu

      ADMIN_FILE=/etc/stalwart/admin-password
      RELAY_FILE=/etc/stalwart/relay-password

      for f in "$ADMIN_FILE" "$RELAY_FILE"; do
        if [ ! -s "$f" ]; then
          echo "ERROR: required password file missing or empty: $f" >&2
          echo "  See docs/mail/setup.md (step 6: provision relay account)." >&2
          echo "  Place a strong password with:" >&2
          echo "    install -m 0400 -o root /dev/stdin $f <<< \"<strong-password>\"" >&2
          echo "  Then: systemctl restart stalwart-relay-account" >&2
          exit 1
        fi
      done

      ADMIN=$(cat "$ADMIN_FILE")
      PLAIN=$(cat "$RELAY_FILE")
      BCRYPT=$(mkpasswd -sm bcrypt "$PLAIN")
      DOMAIN="${cfg.domain}"
      API="http://127.0.0.1:8088/api/principal"

      # Wait up to 30s for Stalwart admin API.
      for _ in $(seq 1 30); do
        if curl -fsS -o /dev/null -u "admin:$ADMIN" "$API" 2>/dev/null; then
          break
        fi
        sleep 1
      done

      # Stalwart's admin API returns 200 with {"data": null} when a
      # principal does not exist (not 404). Detect existence by reading
      # whether .data.id is present in the body.
      EXISTS=$(curl -fsS -u "admin:$ADMIN" "$API/relay" | jq -r 'if (.data.id // null) != null then "yes" else "no" end')

      if [ "$EXISTS" = "no" ]; then
        BODY=$(jq -nc \
          --arg email "relay@$DOMAIN" \
          --arg secret "$BCRYPT" \
          '{type:"individual",name:"relay",emails:[$email],secrets:[$secret],description:"Brygge SMTP send-only",roles:["user"],quota:1}')
        curl -fsS -u "admin:$ADMIN" -X POST "$API" -H 'Content-Type: application/json' -d "$BODY"
      else
        BODY=$(jq -nc --arg secret "$BCRYPT" \
          '[{action:"set",field:"secrets",value:[$secret]}]')
        curl -fsS -u "admin:$ADMIN" -X PATCH "$API/relay" -H 'Content-Type: application/json' -d "$BODY"
      fi
    '';
  };

  # Bulwark webmail (JMAP client for Stalwart). Runs in a container.
  virtualisation.oci-containers = {
    backend = "podman";
    containers.bulwark = {
      image = "ghcr.io/bulwarkmail/webmail:latest";
      ports = [ "127.0.0.1:3000:3000" ];
      environment = {
        JMAP_SERVER_URL = "https://mail.${cfg.domain}";
      };
      autoStart = true;
    };
  };

  virtualisation.podman = {
    enable = true;
    dockerCompat = false;
    defaultNetwork.settings.dns_enabled = true;
  };

  system.autoUpgrade = {
    enable = false;
  };

  services.journald.extraConfig = ''
    SystemMaxUse=500M
    MaxRetentionSec=1month
  '';

  # Tailscale — used as the network path for forwarding telemetry to the
  # home cluster's OTel stack. Run `tailscale up` once on first deploy
  # to authenticate the node into the tailnet.
  services.tailscale = {
    enable = true;
    openFirewall = true;
    # accept-dns: use the tailnet's DNS resolver so MagicDNS hostnames resolve.
    # accept-routes: accept subnet routes advertised by other peers (e.g. a
    # gateway router exporting LAN access for the in-cluster collector).
    # advertise-tags: brygge identifies as a downstream node — the tailnet ACL
    # allows other hosts to dial in but blocks brygge from initiating
    # connections to them (so a compromised public VM can't pivot inward).
    # If the control server (Headscale URL) ever needs (re-)setting, do that
    # interactively on the host: `tailscale up --login-server=<url>`.
    extraUpFlags = [
      "--accept-dns=true"
      "--accept-routes=true"
      "--advertise-tags=tag:downstream"
    ];
  };

  # OpenTelemetry collector. Two responsibilities:
  #   1. Scrape VM system metrics (CPU, memory, disk, network, processes)
  #   2. Receive OTLP from brygge on 127.0.0.1:4317 and forward to the
  #      home cluster's OTel stack (endpoint set via /etc/otel/env)
  services.opentelemetry-collector = {
    enable = true;
    package = pkgs.opentelemetry-collector-contrib;
    settings = {
      receivers = {
        hostmetrics = {
          collection_interval = "30s";
          scrapers = {
            cpu = { };
            memory = { };
            disk = { };
            filesystem = { };
            load = { };
            network = { };
            paging = { };
            processes = { };
          };
        };
        otlp = {
          protocols = {
            grpc.endpoint = "127.0.0.1:4317";
            http.endpoint = "127.0.0.1:4318";
          };
        };
        journald = {
          units = [
            "brygge.service"
            "stalwart.service"
            "caddy.service"
            "postgresql.service"
            "podman-bulwark.service"
          ];
        };
      };
      processors = {
        batch = {
          timeout = "10s";
          send_batch_size = 1024;
        };
        resourcedetection = {
          detectors = [ "system" "env" ];
        };
        resource = {
          attributes = [
            { key = "host.name"; value = clubConfig.hostname; action = "upsert"; }
            { key = "service.namespace"; value = clubConfig.domain; action = "upsert"; }
          ];
        };
      };
      exporters = {
        # Endpoint and auth come from /etc/otel/env (root-only, not committed):
        #   OTLP_ENDPOINT=collector.example.com:4317
        #   OTLP_AUTH_HEADER=Bearer <token>
        # No scheme on endpoint — gRPC OTLP exporter takes host:port directly.
        # tls.insecure only disables transport TLS; if the path is over a
        # private network (e.g. Tailscale's WireGuard tunnel) transport TLS
        # is redundant. The bearer token still rides in gRPC metadata
        # headers regardless of tls.insecure.
        "otlp/upstream" = {
          endpoint = "\${env:OTLP_ENDPOINT}";
          tls.insecure = true;
          headers = {
            Authorization = "\${env:OTLP_AUTH_HEADER}";
          };
        };
      };
      service = {
        pipelines = {
          metrics = {
            receivers = [ "hostmetrics" "otlp" ];
            processors = [ "resourcedetection" "resource" "batch" ];
            exporters = [ "otlp/upstream" ];
          };
          traces = {
            receivers = [ "otlp" ];
            processors = [ "resourcedetection" "resource" "batch" ];
            exporters = [ "otlp/upstream" ];
          };
          logs = {
            receivers = [ "otlp" "journald" ];
            processors = [ "resourcedetection" "resource" "batch" ];
            exporters = [ "otlp/upstream" ];
          };
        };
        telemetry = {
          # Suppress the collector's own internal metrics endpoint to
          # avoid yet another exporter loop.
          metrics.level = "none";
        };
      };
    };
  };

  systemd.services.opentelemetry-collector.serviceConfig = {
    # /etc/otel/env contains HOME_OTLP_ENDPOINT and (optionally)
    # HOME_OTLP_AUTH_HEADER — managed by the deployer, not Nix.
    EnvironmentFile = "/etc/otel/env";
    # Read journald — needs the systemd-journal group.
    SupplementaryGroups = [ "systemd-journal" ];
  };

  # Brygge sends OTLP to the local collector. Per-club tagging
  # (club.slug, club.domain) is set by brygge itself from /etc/brygge/env;
  # see telemetry.Options. Sampler arg overrides the in-code default (0.1) —
  # bump during incident debugging if needed.
  services.brygge.extraEnvironment = {
    OTEL_EXPORTER_OTLP_ENDPOINT = "http://127.0.0.1:4317";
    OTEL_EXPORTER_OTLP_PROTOCOL = "grpc";
    OTEL_SERVICE_NAME = "brygge-api";
    OTEL_TRACES_SAMPLER_ARG = "0.1";
    # Magic-link / share URLs use this as the public origin. Without it,
    # config defaults to the dev Vite URL (http://localhost:5173).
    FRONTEND_URL = "https://${clubConfig.domain}";
    # Per-club identity — sourced from clubConfig (tfvars) instead of
    # /etc/brygge/env to eliminate the silent-fallback failure mode
    # (missing CLUB_NAME → NavBar shows "Brygge", emails say "your club").
    CLUB_SLUG = clubConfig.slug;
    CLUB_NAME = clubConfig.name;
  };
}
