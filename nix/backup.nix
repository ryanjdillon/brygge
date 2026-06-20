{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.brygge.backup;
  parentCfg = config.services.brygge;

  # Shared pg_dump + mc upload logic used by both GFS and pre-deploy scripts.
  # Arguments: $1 = destination prefix path (e.g. "bryggebackup/bucket/daily")
  #            $2 = dump file path
  commonInputs = [
    pkgs.postgresql
    pkgs.minio-client
    pkgs.coreutils
    pkgs.curl
  ];

  gfsBackupScript = pkgs.writeShellApplication {
    name = "brygge-backup";

    runtimeInputs = commonInputs;

    text = ''
      set -euo pipefail

      : "''${DATABASE_URL:?DATABASE_URL must be set}"
      : "''${BACKUP_S3_ACCESS_KEY:?BACKUP_S3_ACCESS_KEY must be set}"
      : "''${BACKUP_S3_SECRET_KEY:?BACKUP_S3_SECRET_KEY must be set}"

      S3_ENDPOINT="''${BACKUP_S3_ENDPOINT:-${cfg.s3Endpoint}}"
      S3_BUCKET="''${BACKUP_S3_BUCKET:-${cfg.s3Bucket}}"
      S3_PREFIX="''${BACKUP_S3_PREFIX:-${cfg.s3Prefix}}"

      KEEP_DAILY="''${BACKUP_KEEP_DAILY:-${toString cfg.retention.daily}}"
      KEEP_WEEKLY="''${BACKUP_KEEP_WEEKLY:-${toString cfg.retention.weekly}}"
      KEEP_MONTHLY="''${BACKUP_KEEP_MONTHLY:-${toString cfg.retention.monthly}}"

      TIMESTAMP="$(date -u +%Y%m%d-%H%M%S)"
      DOW="$(date -u +%u)"
      DOM="$(date -u +%d)"

      FILENAME="brygge-''${TIMESTAMP}.dump"

      PREFIXES=("daily")
      if [[ "''${DOW}" == "1" ]]; then
        PREFIXES+=("weekly")
      fi
      if [[ "''${DOM}" == "01" ]]; then
        PREFIXES+=("monthly")
      fi

      WORKDIR="$(mktemp -d)"
      trap 'rm -rf "''${WORKDIR}"' EXIT
      DUMPFILE="''${WORKDIR}/''${FILENAME}"

      echo "brygge-backup: dumping database…"
      pg_dump \
        --format=custom \
        --compress=9 \
        --no-owner \
        --no-privileges \
        "''${DATABASE_URL}" \
        --file="''${DUMPFILE}"
      echo "brygge-backup: dump complete ($(du -h "''${DUMPFILE}" | cut -f1))"

      export MC_CONFIG_DIR="''${WORKDIR}/.mc"
      mc alias set bryggebackup \
        "''${S3_ENDPOINT}" \
        "''${BACKUP_S3_ACCESS_KEY}" \
        "''${BACKUP_S3_SECRET_KEY}" \
        --quiet

      for PREFIX in "''${PREFIXES[@]}"; do
        DEST="bryggebackup/''${S3_BUCKET}/''${S3_PREFIX}/''${PREFIX}/''${FILENAME}"
        echo "brygge-backup: uploading to ''${DEST}…"
        mc cp --quiet "''${DUMPFILE}" "''${DEST}"
        echo "brygge-backup: upload OK → ''${PREFIX}/"
      done

      prune_tier() {
        local prefix="''${1}"
        local keep="''${2}"

        mapfile -t OBJECTS < <(
          mc ls --quiet "bryggebackup/''${S3_BUCKET}/''${S3_PREFIX}/''${prefix}/" 2>/dev/null \
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
          mc rm --quiet "bryggebackup/''${S3_BUCKET}/''${S3_PREFIX}/''${prefix}/''${obj}"
          echo "brygge-backup: removed ''${prefix}/''${obj}"
        done
      }

      prune_tier "daily"   "''${KEEP_DAILY}"
      prune_tier "weekly"  "''${KEEP_WEEKLY}"
      prune_tier "monthly" "''${KEEP_MONTHLY}"

      if [[ -n "''${BACKUP_HEALTH_PING_URL:-}" ]]; then
        echo "brygge-backup: pinging health check URL…"
        curl -fsS "''${BACKUP_HEALTH_PING_URL}" > /dev/null
        echo "brygge-backup: health ping OK"
      fi

      echo "brygge-backup: all done."
    '';
  };

  preDeployScript = pkgs.writeShellApplication {
    name = "brygge-pre-deploy-backup";

    runtimeInputs = [ pkgs.postgresql pkgs.coreutils ];

    text = ''
      set -euo pipefail

      : "''${DATABASE_URL:?DATABASE_URL must be set}"

      LOCAL_DIR="${cfg.preDeployBackup.localDir}"
      KEEP="${toString cfg.preDeployBackup.keep}"

      TIMESTAMP="$(date -u +%Y%m%d-%H%M%S)"
      DUMPFILE="''${LOCAL_DIR}/brygge-predeploy-''${TIMESTAMP}.dump"

      mkdir -p "''${LOCAL_DIR}"

      echo "brygge-pre-deploy-backup: dumping database…"
      pg_dump \
        --format=custom \
        --compress=9 \
        --no-owner \
        --no-privileges \
        "''${DATABASE_URL}" \
        --file="''${DUMPFILE}"
      echo "brygge-pre-deploy-backup: dump complete ($(du -h "''${DUMPFILE}" | cut -f1))"

      # Prune oldest dumps beyond keep count.
      mapfile -t ALL < <(ls -t "''${LOCAL_DIR}"/brygge-predeploy-*.dump 2>/dev/null || true)
      TOTAL="''${#ALL[@]}"
      if [[ "''${TOTAL}" -gt "''${KEEP}" ]]; then
        TO_REMOVE=$(( TOTAL - KEEP ))
        echo "brygge-pre-deploy-backup: pruning ''${TO_REMOVE} old dump(s) (keep=''${KEEP})"
        for (( i=KEEP; i<TOTAL; i++ )); do
          rm -f "''${ALL[''${i}]}"
          echo "brygge-pre-deploy-backup: removed ''${ALL[''${i}]}"
        done
      fi

      echo "brygge-pre-deploy-backup: done. Latest: ''${DUMPFILE}"
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
      example = "https://nbg1.your-objectstorage.com";
      description = "S3-compatible endpoint URL.";
    };

    s3Bucket = lib.mkOption {
      type = lib.types.str;
      example = "brygge-backups";
      description = "S3 bucket name.";
    };

    s3Prefix = lib.mkOption {
      type = lib.types.str;
      default = "backups";
      example = "brygge/backups";
      description = ''
        Key prefix inside the bucket. GFS dumps land at
        <s3Prefix>/daily/, <s3Prefix>/weekly/, <s3Prefix>/monthly/.
        Use a non-empty prefix when sharing a bucket with other data
        (e.g. Terraform state).
      '';
    };

    s3AccessKey = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = ''
        S3 access key ID. When set, injected directly into the systemd
        environment so the env file does not need to carry backup credentials
        separately. Leave empty to supply BACKUP_S3_ACCESS_KEY via environmentFile.
      '';
    };

    s3SecretKey = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = ''
        S3 secret access key. Same semantics as s3AccessKey.
        Leave empty to supply BACKUP_S3_SECRET_KEY via environmentFile.
      '';
    };

    databaseUrl = lib.mkOption {
      type = lib.types.str;
      default = parentCfg.databaseUrl;
      defaultText = lib.literalExpression "config.services.brygge.databaseUrl";
      description = "Postgres DSN used by pg_dump.";
    };

    environmentFile = lib.mkOption {
      type = lib.types.path;
      default = parentCfg.environmentFile;
      defaultText = lib.literalExpression "config.services.brygge.environmentFile";
      description = ''
        systemd EnvironmentFile supplying S3 credentials.

        Required keys:
          BACKUP_S3_ACCESS_KEY
          BACKUP_S3_SECRET_KEY
      '';
    };

    schedule = lib.mkOption {
      type = lib.types.str;
      default = "*-*-* 02:30:00";
      description = "systemd OnCalendar expression for the nightly GFS backup timer.";
    };

    retention = lib.mkOption {
      description = "GFS retention counts per tier.";
      default = { };
      type = lib.types.submodule {
        options = {
          daily = lib.mkOption {
            type = lib.types.ints.positive;
            default = 7;
            description = "Daily backups to keep.";
          };
          weekly = lib.mkOption {
            type = lib.types.ints.positive;
            default = 4;
            description = "Weekly backups to keep (written on Mondays).";
          };
          monthly = lib.mkOption {
            type = lib.types.ints.positive;
            default = 12;
            description = "Monthly backups to keep (written on the 1st).";
          };
        };
      };
    };

    healthPingUrl = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Uptime-Kuma push URL pinged on successful GFS backup. Null disables.";
    };

    preDeployBackup = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Enable the brygge-pre-deploy-backup oneshot service.
          The deploy script starts this before deploy-rs so there is
          always a quiesced snapshot to roll back to if a migration
          goes wrong.
        '';
      };

      localDir = lib.mkOption {
        type = lib.types.str;
        default = "/var/lib/brygge/backups/pre-deploy";
        description = "Directory on the server where pre-deploy dumps are stored.";
      };

      keep = lib.mkOption {
        type = lib.types.ints.positive;
        default = 10;
        description = "Number of pre-deploy dumps to keep on the server.";
      };
    };
  };

  config = lib.mkIf cfg.enable {

    # ── Nightly GFS backup → S3 ──────────────────────────────────────────
    systemd.services.brygge-backup = {
      description = "brygge — S3 database backup (pg_dump custom format, GFS retention)";

      after = [ "postgresql.service" "network-online.target" ];
      requires = [ "postgresql.service" ];
      wants = [ "network-online.target" ];

      environment = {
        DATABASE_URL = cfg.databaseUrl;
        BACKUP_S3_ENDPOINT = cfg.s3Endpoint;
        BACKUP_S3_BUCKET = cfg.s3Bucket;
        BACKUP_S3_PREFIX = cfg.s3Prefix;
        BACKUP_KEEP_DAILY = toString cfg.retention.daily;
        BACKUP_KEEP_WEEKLY = toString cfg.retention.weekly;
        BACKUP_KEEP_MONTHLY = toString cfg.retention.monthly;
      }
      // lib.optionalAttrs (cfg.s3AccessKey != "") {
        BACKUP_S3_ACCESS_KEY = cfg.s3AccessKey;
      }
      // lib.optionalAttrs (cfg.s3SecretKey != "") {
        BACKUP_S3_SECRET_KEY = cfg.s3SecretKey;
      }
      // lib.optionalAttrs (cfg.healthPingUrl != null) {
        BACKUP_HEALTH_PING_URL = cfg.healthPingUrl;
      };

      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = false;
        User = "brygge";
        Group = "brygge";
        EnvironmentFile = cfg.environmentFile;
        ExecStart = "${gfsBackupScript}/bin/brygge-backup";

        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        RestrictRealtime = true;
        SystemCallArchitectures = "native";
      };
    };

    systemd.timers.brygge-backup = {
      description = "brygge — S3 database backup timer";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = cfg.schedule;
        Persistent = true;
        Unit = "brygge-backup.service";
      };
    };

    # ── Pre-deploy local backup ───────────────────────────────────────────
    systemd.services.brygge-pre-deploy-backup = lib.mkIf cfg.preDeployBackup.enable {
      description = "brygge — pre-deploy database snapshot (local)";

      after = [ "postgresql.service" ];
      requires = [ "postgresql.service" ];

      environment = {
        DATABASE_URL = cfg.databaseUrl;
      };

      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = false;
        User = "brygge";
        Group = "brygge";
        EnvironmentFile = cfg.environmentFile;
        ExecStart = "${preDeployScript}/bin/brygge-pre-deploy-backup";

        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_UNIX" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        RestrictRealtime = true;
        SystemCallArchitectures = "native";
        # Pre-deploy dumps land in /var/lib/brygge which is the service's
        # StateDirectory — ProtectSystem=strict requires ReadWritePaths for
        # any path the script writes to outside of /tmp.
        ReadWritePaths = [ cfg.preDeployBackup.localDir ];
      };
    };
  };
}
