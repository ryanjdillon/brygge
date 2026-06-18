{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.brygge.backup;
  parentCfg = config.services.brygge;

  backupScript = pkgs.writeShellApplication {
    name = "brygge-backup";

    runtimeInputs = [
      pkgs.postgresql
      pkgs.minio-client
      pkgs.coreutils
      pkgs.curl
    ];

    text = ''
      set -euo pipefail

      # ── Validate required env vars ────────────────────────────────────
      : "''${DATABASE_URL:?DATABASE_URL must be set}"
      : "''${BACKUP_S3_ACCESS_KEY:?BACKUP_S3_ACCESS_KEY must be set (from environmentFile)}"
      : "''${BACKUP_S3_SECRET_KEY:?BACKUP_S3_SECRET_KEY must be set (from environmentFile)}"

      # Non-secret options may also be overridden via env.
      S3_ENDPOINT="''${BACKUP_S3_ENDPOINT:-${cfg.s3Endpoint}}"
      S3_BUCKET="''${BACKUP_S3_BUCKET:-${cfg.s3Bucket}}"

      KEEP_DAILY="''${BACKUP_KEEP_DAILY:-${toString cfg.retention.daily}}"
      KEEP_WEEKLY="''${BACKUP_KEEP_WEEKLY:-${toString cfg.retention.weekly}}"
      KEEP_MONTHLY="''${BACKUP_KEEP_MONTHLY:-${toString cfg.retention.monthly}}"

      # ── Timestamp and GFS tier selection ─────────────────────────────
      TIMESTAMP="$(date -u +%Y%m%d-%H%M%S)"
      DOW="$(date -u +%u)"   # 1=Mon … 7=Sun
      DOM="$(date -u +%d)"   # 01-31

      FILENAME="brygge-''${TIMESTAMP}.dump"

      # Determine which prefixes this run writes to.
      PREFIXES=("daily")
      if [[ "''${DOW}" == "1" ]]; then
        PREFIXES+=("weekly")
      fi
      if [[ "''${DOM}" == "01" ]]; then
        PREFIXES+=("monthly")
      fi

      # ── Work directory (private, cleaned up on exit) ──────────────────
      WORKDIR="$(mktemp -d)"
      trap 'rm -rf "''${WORKDIR}"' EXIT
      DUMPFILE="''${WORKDIR}/''${FILENAME}"

      # ── Dump ─────────────────────────────────────────────────────────
      echo "brygge-backup: dumping database…"
      pg_dump \
        --format=custom \
        --compress=9 \
        --no-owner \
        --no-privileges \
        "''${DATABASE_URL}" \
        --file="''${DUMPFILE}"

      echo "brygge-backup: dump complete ($(du -h "''${DUMPFILE}" | cut -f1))"

      # ── Configure mc alias ───────────────────────────────────────────
      # Use a temporary config dir so we don't touch any persistent state.
      export MC_CONFIG_DIR="''${WORKDIR}/.mc"
      mc alias set bryggebackup \
        "''${S3_ENDPOINT}" \
        "''${BACKUP_S3_ACCESS_KEY}" \
        "''${BACKUP_S3_SECRET_KEY}" \
        --quiet

      # ── Upload to every applicable GFS tier ──────────────────────────
      for PREFIX in "''${PREFIXES[@]}"; do
        DEST="bryggebackup/''${S3_BUCKET}/''${PREFIX}/''${FILENAME}"
        echo "brygge-backup: uploading to ''${DEST}…"
        mc cp --quiet "''${DUMPFILE}" "''${DEST}"
        echo "brygge-backup: upload OK → ''${PREFIX}/"
      done

      # ── Retention: prune each tier to its configured count ───────────
      # List objects newest-first (mc ls sorts ascending; we reverse),
      # then remove anything beyond the keep count.

      prune_tier() {
        local prefix="''${1}"
        local keep="''${2}"

        # Collect all object paths under this prefix, sorted newest-first.
        mapfile -t OBJECTS < <(
          mc ls --quiet "bryggebackup/''${S3_BUCKET}/''${prefix}/" 2>/dev/null \
            | awk '{print $NF}' \
            | sort -r
        )

        local total="''${#OBJECTS[@]}"
        if [[ "''${total}" -le "''${keep}" ]]; then
          echo "brygge-backup: ''${prefix}/ has ''${total} object(s), keep=''${keep} — nothing to prune"
          return
        fi

        local to_remove=$(( total - keep ))
        echo "brygge-backup: ''${prefix}/ has ''${total} object(s), pruning ''${to_remove} (keep=''${keep})"

        for (( i=keep; i<total; i++ )); do
          local obj="''${OBJECTS[''${i}]}"
          mc rm --quiet "bryggebackup/''${S3_BUCKET}/''${prefix}/''${obj}"
          echo "brygge-backup: removed ''${prefix}/''${obj}"
        done
      }

      prune_tier "daily"   "''${KEEP_DAILY}"
      prune_tier "weekly"  "''${KEEP_WEEKLY}"
      prune_tier "monthly" "''${KEEP_MONTHLY}"

      # ── Health ping on success ────────────────────────────────────────
      if [[ -n "''${BACKUP_HEALTH_PING_URL:-}" ]]; then
        echo "brygge-backup: pinging health check URL…"
        curl -fsS "''${BACKUP_HEALTH_PING_URL}" > /dev/null
        echo "brygge-backup: health ping OK"
      fi

      echo "brygge-backup: all done."
    '';
  };

in
{
  options.services.brygge.backup = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable automated scheduled Postgres backups to S3-compatible storage.";
    };

    s3Endpoint = lib.mkOption {
      type = lib.types.str;
      example = "https://s3.eu-central-003.backblazeb2.com";
      description = ''
        S3-compatible endpoint URL (non-secret). May be overridden at
        runtime via BACKUP_S3_ENDPOINT in the environmentFile.
      '';
    };

    s3Bucket = lib.mkOption {
      type = lib.types.str;
      example = "brygge-backups";
      description = ''
        S3 bucket name (non-secret). May be overridden at runtime via
        BACKUP_S3_BUCKET in the environmentFile.
      '';
    };

    databaseUrl = lib.mkOption {
      type = lib.types.str;
      default = parentCfg.databaseUrl;
      defaultText = lib.literalExpression "config.services.brygge.databaseUrl";
      description = ''
        Postgres DSN used by pg_dump. Defaults to the parent service DSN
        (unix socket, peer auth). The brygge system user must have a
        matching PostgreSQL role with read access — the same peer-auth
        requirement that applies to brygge-migrate.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.path;
      default = parentCfg.environmentFile;
      defaultText = lib.literalExpression "config.services.brygge.environmentFile";
      description = ''
        Path to a root-only (chmod 0400) systemd EnvironmentFile that
        supplies backup credentials, kept separate from the main app env
        so the backup bucket/credentials are independent.

        Required keys:
          BACKUP_S3_ACCESS_KEY  — S3 access key ID
          BACKUP_S3_SECRET_KEY  — S3 secret access key

        Optional override keys (take precedence over the non-secret options):
          BACKUP_S3_ENDPOINT    — override services.brygge.backup.s3Endpoint
          BACKUP_S3_BUCKET      — override services.brygge.backup.s3Bucket
      '';
    };

    schedule = lib.mkOption {
      type = lib.types.str;
      default = "*-*-* 02:30:00";
      description = ''
        Systemd OnCalendar expression for the backup timer.
        Default is daily at 02:30 UTC.
      '';
      example = "Mon *-*-* 03:00:00";
    };

    retention = lib.mkOption {
      description = "GFS (Grandfather-Father-Son) retention counts per tier.";
      default = { };
      type = lib.types.submodule {
        options = {
          daily = lib.mkOption {
            type = lib.types.ints.positive;
            default = 7;
            description = "Number of daily backups to keep.";
          };
          weekly = lib.mkOption {
            type = lib.types.ints.positive;
            default = 4;
            description = "Number of weekly backups to keep (written on Mondays).";
          };
          monthly = lib.mkOption {
            type = lib.types.ints.positive;
            default = 12;
            description = "Number of monthly backups to keep (written on the 1st).";
          };
        };
      };
    };

    healthPingUrl = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "https://uptime.example.com/api/push/abc123";
      description = ''
        Uptime-Kuma (or compatible) push URL pinged via curl on a
        successful backup. When set, a missed ping indicates a failed
        backup job. Leave null to disable.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.brygge-backup = {
      description = "brygge — S3 database backup (pg_dump custom format, GFS retention)";

      # No wantedBy — started exclusively by the timer.
      after = [ "postgresql.service" "network-online.target" ];
      requires = [ "postgresql.service" ];
      wants = [ "network-online.target" ];

      # NOTE: if you want an alert on failure (in addition to the missed
      # Kuma ping), wire up an OnFailure= unit here, e.g.:
      #   OnFailure = "brygge-backup-notify-failure@%n.service";

      environment = {
        DATABASE_URL = cfg.databaseUrl;
        # Non-secret S3 config; may be overridden via env vars in the file.
        BACKUP_S3_ENDPOINT = cfg.s3Endpoint;
        BACKUP_S3_BUCKET = cfg.s3Bucket;
        # Retention counts baked in at build time; still overrideable via env.
        BACKUP_KEEP_DAILY = toString cfg.retention.daily;
        BACKUP_KEEP_WEEKLY = toString cfg.retention.weekly;
        BACKUP_KEEP_MONTHLY = toString cfg.retention.monthly;
      }
      // lib.optionalAttrs (cfg.healthPingUrl != null) {
        BACKUP_HEALTH_PING_URL = cfg.healthPingUrl;
      };

      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = false;

        # The brygge system user has peer-auth access to the postgres socket
        # (same as brygge-migrate). Ensure the PostgreSQL role exists with
        # CONNECT + SELECT privileges on the brygge database.
        User = "brygge";
        Group = "brygge";

        EnvironmentFile = cfg.environmentFile;

        ExecStart = "${backupScript}/bin/brygge-backup";

        # ── Hardening ────────────────────────────────────────────────────
        # Network access is required for S3 uploads; AF_UNIX is required
        # for the postgres unix socket.
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
        RestrictRealtime = true;
        SystemCallArchitectures = "native";
        # MemoryDenyWriteExecute is intentionally omitted: bash and mc use
        # writable executable memory (JIT / trampolines).
      };
    };

    systemd.timers.brygge-backup = {
      description = "brygge — S3 database backup timer";
      wantedBy = [ "timers.target" ];

      timerConfig = {
        OnCalendar = cfg.schedule;
        # Persistent=true means a missed run (e.g. while the host was down)
        # will fire as soon as the system comes back up.
        Persistent = true;
        Unit = "brygge-backup.service";
      };
    };
  };
}
