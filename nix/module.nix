{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.brygge;
in
{
  options.services.brygge = {
    enable = lib.mkEnableOption "brygge harbor club platform";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The brygge package to use (provides /bin/brygge and /bin/brygge-seed).";
    };

    domain = lib.mkOption {
      type = lib.types.str;
      default = "example.invalid";
      example = "klubb.no";
      description = ''
        Primary domain (used for Caddy virtualhosts, Matrix server name,
        cookies, CORS, and email links). Override in
        nix/configuration.local.nix before deploying.
      '';
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 8080;
      description = "HTTP port the API listens on (bound to 127.0.0.1).";
    };

    databaseUrl = lib.mkOption {
      type = lib.types.str;
      default = "postgres:///brygge?host=/run/postgresql&sslmode=disable";
      description = ''
        Postgres DSN. Default uses the Unix socket managed by the local
        postgresql service; override via environmentFile for remote DBs.
      '';
    };

    redisUrl = lib.mkOption {
      type = lib.types.str;
      default = "unix:///run/redis-brygge/redis.sock";
      description = "Redis URL. Default is the local redis-brygge unix socket.";
    };

    environmentFile = lib.mkOption {
      type = lib.types.path;
      description = ''
        Path to a systemd EnvironmentFile containing secret values
        (JWT_SECRET, VIPPS_*, SMTP_*, S3 creds, etc.).
        Must be readable only by root (chmod 0400).
      '';
    };

    extraEnvironment = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      description = "Extra non-secret environment variables.";
    };

    features = lib.mkOption {
      type = lib.types.attrsOf lib.types.bool;
      default = {
        bookings = true;
        projects = true;
        calendar = true;
        commerce = true;
        communications = true;
      };
      description = "Feature flag toggles (mapped to FEATURE_* env vars).";
    };

    migrate = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Run golang-migrate against the DB before starting the API.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.brygge = {
      isSystemUser = true;
      group = "brygge";
      home = "/var/lib/brygge";
      createHome = true;
    };
    users.groups.brygge = { };

    systemd.services.brygge-migrate = lib.mkIf cfg.migrate.enable {
      description = "brygge — apply database migrations";
      wantedBy = [ "brygge.service" ];
      before = [ "brygge.service" ];
      after = [ "postgresql.service" ];
      requires = [ "postgresql.service" ];

      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = false;
        User = "brygge";
        Group = "brygge";
        EnvironmentFile = cfg.environmentFile;
      };

      environment = {
        DATABASE_URL = cfg.databaseUrl;
      };

      script = ''
        ${pkgs.go-migrate}/bin/migrate \
          -path ${cfg.package.migrations} \
          -database "''${DATABASE_URL}" \
          up
      '';
    };

    systemd.services.brygge = {
      description = "brygge — harbor club platform";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network-online.target"
        "postgresql.service"
        "redis-brygge.service"
      ] ++ lib.optional cfg.migrate.enable "brygge-migrate.service";
      requires = [
        "postgresql.service"
        "redis-brygge.service"
      ] ++ lib.optional cfg.migrate.enable "brygge-migrate.service";
      wants = [ "network-online.target" ];

      environment = {
        PORT = toString cfg.port;
        HOST = "127.0.0.1";
        DOMAIN = cfg.domain;
        DATABASE_URL = cfg.databaseUrl;
        REDIS_URL = cfg.redisUrl;
      }
      // lib.mapAttrs' (name: enabled:
        lib.nameValuePair "FEATURE_${lib.toUpper name}" (if enabled then "true" else "false")
      ) cfg.features
      // cfg.extraEnvironment;

      serviceConfig = {
        ExecStart = "${cfg.package}/bin/brygge";
        EnvironmentFile = cfg.environmentFile;

        User = "brygge";
        Group = "brygge";
        StateDirectory = "brygge";
        WorkingDirectory = "/var/lib/brygge";

        Restart = "on-failure";
        RestartSec = 5;

        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [
          "AF_UNIX"
          "AF_INET"
          "AF_INET6"
        ];
        RestrictNamespaces = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictRealtime = true;
        SystemCallArchitectures = "native";
      };
    };
  };
}
