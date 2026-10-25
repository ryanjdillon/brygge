{
  description = "Brygge — harbor club platform";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    # Pinned to the pre-2026-09 rev that still ships stalwart-cli 0.15.x bundled
    # in the `stalwart` package. Newer nixpkgs split stalwart-cli into its own
    # package AND jumped it to a 1.x generic-object CLI with no `dkim`
    # subcommand, which doesn't match our 0.15.5 server. Used ONLY for the DKIM
    # provisioning CLI (stalwart-dkim-config.service); the server and everything
    # else track the main nixpkgs. Remove when the mail server is modernised
    # (BRY-278). NOT following nixpkgs — it must stay on the old rev.
    nixpkgs-stalwart-cli.url = "github:NixOS/nixpkgs/b12141ef619e0a9c1c84dc8c684040326f27cdcc";

    terranix.url = "github:terranix/terranix";
    terranix.inputs.nixpkgs.follows = "nixpkgs";

    disko.url = "github:nix-community/disko";
    disko.inputs.nixpkgs.follows = "nixpkgs";

    deploy-rs.url = "github:serokell/deploy-rs";
    deploy-rs.inputs.nixpkgs.follows = "nixpkgs";

    nixos-anywhere.url = "github:nix-community/nixos-anywhere";
    nixos-anywhere.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    self,
    nixpkgs,
    nixpkgs-stalwart-cli,
    flake-utils,
    terranix,
    disko,
    deploy-rs,
    nixos-anywhere,
  }: let
    deploySystem = "x86_64-linux";

    # terraform/terraform.tfvars.json is tracked by git with placeholder
    # values so the flake can read it in pure eval. Each deployer edits
    # it with their real values; the file shows as "modified" in git
    # status — that's expected. Nix reads the working copy directly,
    # so any technique that hides local edits from git (skip-worktree,
    # assume-unchanged) also hides them from nix and breaks deploys.
    # The .githooks/pre-commit hook prevents accidentally committing
    # a working-copy version that contains real secrets.
    tfvars = builtins.fromJSON (builtins.readFile ./terraform/terraform.tfvars.json);

    clubConfig = {
      domain       = tfvars.domain;
      # Per-club identity — sourced from tfvars so a deploy doesn't
      # require touching /etc/brygge/env. Fallbacks keep fresh-clone
      # eval working before a deployer fills these in.
      slug         = tfvars.club_slug or "brygge";
      name         = tfvars.club_name or "";
      adminEmail   = tfvars.admin_email;
      adminSshKeys = tfvars.admin_ssh_keys;
      hostname     = tfvars.server_name;
      timezone     = tfvars.timezone;
      # Feature flags. Pass-through to services.brygge.features (a
      # submodule with per-flag defaults), so this can be empty {} or
      # a partial override like {accounting = true;}.
      features     = tfvars.features or { };
      # Shared role-mapped board mailboxes (DIL-275/276). Pure data;
      # rendered to /etc/brygge/board-mailboxes.json by host.nix and
      # consumed by both stalwart-mailbox-config.service and the
      # Brygge backend reconciler.
      boardMailboxes = tfvars.board_mailboxes or [];
      # Hetzner Object Storage — credentials and endpoint shared across buckets.
      s3Endpoint       = tfvars.s3_endpoint or "";
      s3AccessKey      = tfvars.hetzner_s3_access_key or "";
      s3SecretKey      = tfvars.hetzner_s3_secret_key or "";
      s3BucketDocs     = tfvars.s3_bucket_docs or "";
      s3BucketBackups  = tfvars.s3_bucket_backups or "";
      s3BucketLegal    = tfvars.s3_bucket_legal or "";
      # Tailnet DNS suffix (e.g. "tail.example.net"). The local resolver
      # forwards only this zone to MagicDNS so the OTel collector's upstream
      # endpoint resolves; null (unset or empty) forwards nothing.
      tailnetDomain    = let d = tfvars.tailnet_domain or null; in
        if d == "" then null else d;
    };

    overlay = final: prev: {
      brygge = final.callPackage ./nix/package.nix { };

      # nixpkgs builds go-migrate with the snowflake driver linked in, whose
      # init() panics at startup ("failed to parse CA certificate"). Rebuild
      # with only postgres to avoid the bad package-level init.
      go-migrate = prev.go-migrate.overrideAttrs (old: {
        tags = [ "postgres" ];
      });
    };

    mkTerraformConfig = system:
      terranix.lib.terranixConfiguration {
        inherit system;
        modules = [ ./terraform/config.nix ];
      };
  in
    {
      nixosModules.default = ./nix/module.nix;

      overlays.default = overlay;

      nixosConfigurations.brygge = nixpkgs.lib.nixosSystem {
        system = deploySystem;
        specialArgs = {
          inherit self clubConfig;
          # 0.15.x stalwart-cli (matches the 0.15.5 server) from a pinned rev,
          # for the DKIM provisioning service only. See the input comment / BRY-278.
          stalwartCliPkg = nixpkgs-stalwart-cli.legacyPackages.${deploySystem}.stalwart;
        };
        modules = [
          { nixpkgs.overlays = [ overlay ]; }
          disko.nixosModules.disko
          self.nixosModules.default
          ./nix/host.nix
        ];
      };

      deploy.nodes.brygge = {
        hostname = clubConfig.domain;
        sshUser = "root";
        profiles.system = {
          user = "root";
          path = deploy-rs.lib.${deploySystem}.activate.nixos self.nixosConfigurations.brygge;
        };
        remoteBuild = false;
        fastConnection = true;
        # First mailserver activation (rspamd hyperscan init, DKIM key gen,
        # postfix table build) can take >30s, exceeding magic-rollback's
        # default confirmation window. Extend to 5min so the initial deploy
        # doesn't trip into a false rollback.
        confirmTimeout = 300;
        activationTimeout = 300;
      };

      checks = builtins.mapAttrs (system: lib:
        lib.deployChecks self.deploy // {
          mail-bounce = import ./nix/tests/mail-bounce.nix {
            pkgs = nixpkgs.legacyPackages.${system};
          };
          # Same as `just alerts-check`: promtool check + unit tests on the
          # alert rules rendered for a placeholder deployment.
          alerts = let
            pkgs = nixpkgs.legacyPackages.${system};
          in pkgs.runCommand "brygge-alerts-check" {
            nativeBuildInputs = [ pkgs.prometheus.cli ];
          } ''
            BRYGGE_ALERTS_DIR=${./docs/developer/otel/alerts} \
              bash ${./scripts}/check-brygge-alerts.sh
            touch $out
          '';
        }) deploy-rs.lib;
    }
    // flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" ] (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ overlay ];
        };
        terraformConfiguration = mkTerraformConfig system;

        tfInit = ''
          cd "$(${pkgs.git}/bin/git rev-parse --show-toplevel)/terraform"

          export AWS_ACCESS_KEY_ID=$(${pkgs.jq}/bin/jq -r .hetzner_s3_access_key terraform.tfvars.json)
          export AWS_SECRET_ACCESS_KEY=$(${pkgs.jq}/bin/jq -r .hetzner_s3_secret_key terraform.tfvars.json)
          S3_BUCKET=$(${pkgs.jq}/bin/jq -r .s3_bucket terraform.tfvars.json)
          S3_ENDPOINT=$(${pkgs.jq}/bin/jq -r .s3_endpoint terraform.tfvars.json)

          if [[ -e config.tf.json ]]; then rm -f config.tf.json; fi
          cp ${terraformConfiguration} config.tf.json

          ${pkgs.opentofu}/bin/tofu init \
            -backend-config="bucket=$S3_BUCKET" \
            -backend-config="endpoints={s3=\"$S3_ENDPOINT\"}"
        '';
      in {
        packages = {
          default = pkgs.brygge;
          brygge = pkgs.brygge;
          brygge-tools = pkgs.brygge.tools;
          terraformConfiguration = terraformConfiguration;
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            golangci-lint
            nodejs_22
            sqlc
            go-migrate
            postgresql_16
            redis
            docker-compose
            minio-client
            opentofu
            hcloud
            just
            git
            curl
            jq
            # promtool only: the default output's `migrate` would shadow go-migrate.
            prometheus.cli
            deploy-rs.packages.${system}.default
          ];

          shellHook = ''
            echo "brygge dev shell ready" >&2
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"
            export PATH="$PWD/node_modules/.bin:$PATH"
            if [ -n "''${PLAYWRIGHT_DRIVER_PATH:-}" ]; then
              export PLAYWRIGHT_BROWSERS_PATH="''${PLAYWRIGHT_DRIVER_PATH}"
            fi

            # Install the pre-commit hook that blocks committing real
            # secrets in terraform/terraform.tfvars.json. The file will
            # show as "modified" in git status — that's expected; nix
            # flakes read it directly from the working copy.
            if [ -d .githooks ] && [ "$(git config core.hooksPath)" != ".githooks" ]; then
              git config core.hooksPath .githooks
              echo "installed .githooks/ (blocks accidental commits of tfvars.json)"
            fi
          '';
        };

        # One-shot operational tools, run on demand rather than shipped in
        # the deployed system closure. Set DATABASE_URL to target the DB.
        apps.seed = {
          type = "app";
          program = "${pkgs.brygge.tools}/bin/brygge-seed";
          meta.description = "Seed the brygge database (run with DATABASE_URL set)";
        };
        apps.vipps-cutover = {
          type = "app";
          program = "${pkgs.brygge.tools}/bin/vipps-cutover";
          meta.description = "One-off Vipps per-charge cutover (run with DATABASE_URL set)";
        };
        apps.vipps-audit = {
          type = "app";
          program = "${pkgs.brygge.tools}/bin/vipps-audit";
          meta.description = "Find Vipps payments not linked to a faktura, pre-purring (DATABASE_URL + CLUB_ID)";
        };
        apps.vipps-webhook-register = {
          type = "app";
          program = "${pkgs.brygge.tools}/bin/vipps-webhook-register";
          meta.description = "Register/list/delete the Vipps ePayment webhook (run with Vipps credentials set)";
        };
        apps.vipps-report = {
          type = "app";
          program = "${pkgs.brygge.tools}/bin/vipps-report";
          meta.description = "Probe the Vipps Report API — ledgers/funds/fees (run with Vipps credentials set)";
        };

        apps.tf-plan = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-plan" ''
            set -euo pipefail
            ${tfInit}
            ${pkgs.opentofu}/bin/tofu plan "$@"
          '');
          meta.description = "Run `tofu plan` against terraform/ with init pre-handled";
        };

        apps.tf-apply = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-apply" ''
            set -euo pipefail
            ${tfInit}
            ${pkgs.opentofu}/bin/tofu apply "$@"
          '');
          meta.description = "Run `tofu apply` against terraform/ with init pre-handled";
        };

        apps.install = {
          type = "app";
          program = toString (pkgs.writers.writeBash "brygge-install" ''
            set -euo pipefail

            if [[ $# -lt 1 ]]; then
              echo "usage: nix run .#install -- <server-ip-or-host>"
              echo ""
              echo "Bootstraps a fresh Hetzner VM into NixOS using nixos-anywhere."
              echo "Requires the target to be running in Hetzner rescue mode:"
              echo "  hcloud server enable-rescue <server> --type linux64"
              echo "  hcloud server reset <server>"
              exit 1
            fi

            TARGET="root@$1"

            echo "==> pre-building system closure"
            TOPLEVEL=$(${pkgs.nix}/bin/nix build --no-link --print-out-paths \
              .#nixosConfigurations.brygge.config.system.build.toplevel)
            DISKO=$(${pkgs.nix}/bin/nix build --no-link --print-out-paths \
              .#nixosConfigurations.brygge.config.system.build.diskoScript)

            echo "==> installing NixOS onto $TARGET"
            ${nixos-anywhere.packages.${system}.default}/bin/nixos-anywhere \
              --store-paths "$DISKO" "$TOPLEVEL" \
              "$TARGET"
          '');
          meta.description = "Bootstrap a fresh Hetzner VM (rescue mode) into NixOS via nixos-anywhere";
        };

        apps.deploy = {
          type = "app";
          program = toString (pkgs.writers.writeBash "brygge-deploy" ''
            set -euo pipefail

            HOSTNAME="''${1:-}"
            if [[ -z "$HOSTNAME" ]]; then
              echo "usage: nix run .#deploy -- <server-hostname-or-ip> [deploy-rs args]"
              echo "rollback: nix run .#deploy -- <host> --rollback"
              exit 1
            fi
            shift

            # Everything below reads repo-relative paths (tfvars, migrations,
            # scripts/), so run from the checkout root wherever we were called.
            cd "$(${pkgs.git}/bin/git rev-parse --show-toplevel)"

            # ── Preflight: refuse to deploy placeholder club identity ─────
            # terraform/terraform.tfvars.json is committed with PLACEHOLDER
            # values (domain=example.invalid); the deployer fills the working
            # copy with real values. If those go missing — fresh clone, a
            # `git stash`, a reverted checkout — Nix silently builds the
            # placeholder domain. ACME then fails for a bogus TLD, activation
            # aborts, and deploy-rs rolls the LIVE site back onto the previous
            # generation (which may itself be broken). Fail fast here instead
            # of taking a running site down.
            TFVARS="terraform/terraform.tfvars.json"
            if [[ ! -f "$TFVARS" ]]; then
              echo "deploy: ABORT — $TFVARS not found." >&2
              exit 1
            fi
            tf_domain=$(${pkgs.jq}/bin/jq -r '.domain // ""' "$TFVARS")
            tf_admin_email=$(${pkgs.jq}/bin/jq -r '.admin_email // ""' "$TFVARS")
            if [[ -z "$tf_domain" || "$tf_domain" == "example.invalid" || "$tf_domain" == *.invalid ]]; then
              echo "deploy: ABORT — $TFVARS has a placeholder/empty domain ('$tf_domain')." >&2
              echo "deploy: Your real deployer values are not in the working copy — were they stashed?" >&2
              echo "deploy: Recover with:  git stash list  then  git stash pop" >&2
              echo "deploy: Verify with:   ${pkgs.jq}/bin/jq -r .domain $TFVARS   (should be your real domain)" >&2
              echo "deploy: Deploying now would build ACME for a bogus domain, fail activation, and roll" >&2
              echo "deploy: the LIVE site back onto the previous generation. Refusing to continue." >&2
              exit 1
            fi
            if [[ "$tf_admin_email" == *@example.invalid || "$tf_admin_email" == *.invalid ]]; then
              echo "deploy: ABORT — admin_email ('$tf_admin_email') has a placeholder domain; ACME will reject it." >&2
              echo "deploy: Restore real deployer values in $TFVARS before deploying." >&2
              exit 1
            fi

            # Nix flakes copy git-tracked files (including unstaged
            # edits) into the build, but skip untracked files entirely —
            # an untracked source/migration file ships as an invisible
            # no-op (or, for an imported module, a build-time TS2307).
            # Auto-stage anything under the build inputs we care about
            # so the build sees it; the deployer should still commit +
            # push so the deployed artefact matches a repo SHA.
            untracked=$(${pkgs.git}/bin/git ls-files --others --exclude-standard \
                backend/migrations backend/internal backend/cmd backend/pkg \
                frontend/src 2>/dev/null \
              | ${pkgs.gnugrep}/bin/grep -E '\.(sql|go|ts|tsx|vue|js|mjs|json|css)$' || true)
            if [[ -n "$untracked" ]]; then
              echo "deploy: staging untracked source files so the flake build can see them:"
              while IFS= read -r f; do
                echo "  + $f"
                ${pkgs.git}/bin/git add -- "$f"
              done <<< "$untracked"
            fi

            # Surface anything that's tracked-but-uncommitted so the
            # deployer notices their working tree drifted from the repo.
            # The build will pick these edits up regardless; the
            # recommendation is to commit + push so the deployed state
            # reflects the current committed state of the repository.
            uncommitted=$(${pkgs.git}/bin/git status --porcelain -- \
                backend/migrations backend/internal backend/cmd backend/pkg \
                frontend/src 2>/dev/null \
              | ${pkgs.gnugrep}/bin/grep -E '\.(sql|go|ts|tsx|vue|js|mjs|json|css)$' || true)
            if [[ -n "$uncommitted" ]]; then
              echo "deploy: NOTE — uncommitted source changes will be deployed:"
              while IFS= read -r line; do echo "  $line"; done <<< "$uncommitted"
              echo "deploy: please commit and push these so the deployed state matches HEAD."
              echo "deploy: continuing in 5s — Ctrl-C to abort."
              sleep 5
            fi

            # ── Pre-deploy database snapshot ─────────────────────────────
            # Take a quiesced dump before migrations run so there is always a
            # rollback point, and pull a copy to ~/brygge-backups/ on the
            # deployer's machine (last 10 kept). A failed snapshot aborts the
            # deploy: running migrations without one is exactly the risk this
            # guards against. SKIP_BACKUP=1 is the explicit, visible opt-out.
            #
            # The same first ssh also reads prod's schema_migrations for the
            # pending-migration check below, so it costs no extra round trip.
            # The host-side timeout ends psql (and so its transaction) a little
            # before the local one would drop the ssh session.
            DRYRUN=scripts/deploy-migration-dryrun.sh
            export DRYRUN_TIMEOUT="''${DRYRUN_TIMEOUT:-90}"
            if [[ ! "$DRYRUN_TIMEOUT" =~ ^[0-9]+$ ]] || (( DRYRUN_TIMEOUT < 30 )); then
              DRYRUN_TIMEOUT=30
            fi
            PROD_PSQL_REMOTE="timeout --foreground $((DRYRUN_TIMEOUT - 5)) sudo -u brygge psql 'postgres:///brygge?host=/run/postgresql&sslmode=disable'"
            PROD_MIGRATION_STATE=""
            LOCAL_DUMP=""
            if [[ "''${SKIP_BACKUP:-0}" != "1" ]]; then
              PREDEPLOY_DIR=/var/lib/brygge/backups/pre-deploy

              abort_backup() {
                echo "deploy: ERROR — $1"
                echo "deploy: aborting: migrations must not run without a rollback snapshot."
                echo "deploy: fix the cause above, or re-run with SKIP_BACKUP=1 to deploy without one."
                exit 1
              }

              echo "deploy: taking pre-deploy database snapshot on ''${HOSTNAME}…"

              # Create the dump directory before starting the unit: its
              # ReadWritePaths entry fails namespace setup (exit 226) when the
              # path is missing, and a host whose config predates the tmpfiles
              # rule won't have it yet. Also captures the host clock so the
              # dump found below is provably from this run. The state read is
              # best-effort here; the check re-reads it and fails loudly.
              STATE_CMD=""
              if [[ "''${SKIP_PROD_CHECK:-0}" != "1" ]]; then
                STATE_CMD=" && { ''${PROD_PSQL_REMOTE} -X -q -t -A -f - 2>/dev/null || true; }"
              fi
              if ! REMOTE_OUT=$(bash "$DRYRUN" --state-sql | ssh "root@''${HOSTNAME}" \
                  "install -d -o brygge -g brygge -m 0750 ''${PREDEPLOY_DIR%/*} ''${PREDEPLOY_DIR} && date +%s''${STATE_CMD}" 2>&1); then
                echo "''${REMOTE_OUT}"
                abort_backup "could not prepare the backup directory on ''${HOSTNAME}"
              fi
              REMOTE_START=$(${pkgs.gnugrep}/bin/grep -E '^[0-9]+$' <<< "''${REMOTE_OUT}" | head -1 || true)
              if [[ -z "''${REMOTE_START}" ]]; then
                echo "''${REMOTE_OUT}"
                abort_backup "could not read the host clock on ''${HOSTNAME}"
              fi
              PROD_MIGRATION_STATE=$(${pkgs.gnugrep}/bin/grep -E '^([0-9]+[|](true|false)|none)[|](true|false)$' <<< "''${REMOTE_OUT}" | head -1 || true)

              if ! out=$(ssh "root@''${HOSTNAME}" \
                  "systemctl start brygge-pre-deploy-backup.service" 2>&1); then
                echo "''${out}"
                echo "deploy: last log lines of brygge-pre-deploy-backup.service:"
                ssh "root@''${HOSTNAME}" \
                  "journalctl -u brygge-pre-deploy-backup.service -n 30 --no-pager" 2>&1 || true
                abort_backup "pre-deploy backup failed on ''${HOSTNAME}"
              fi

              LATEST=$(ssh "root@''${HOSTNAME}" \
                "find ''${PREDEPLOY_DIR} -maxdepth 1 -name 'brygge-predeploy-*.dump' -newermt @''${REMOTE_START} -printf '%T@ %p\n' | sort -rn | head -1 | cut -d' ' -f2-")
              if [[ -z "''${LATEST}" ]]; then
                abort_backup "backup unit reported success but no new dump appeared in ''${PREDEPLOY_DIR}"
              fi
              echo "deploy: snapshot ready on host: ''${LATEST}"

              LOCAL_BACKUP_DIR="''${HOME}/brygge-backups"
              mkdir -p "''${LOCAL_BACKUP_DIR}"
              DEST="''${LOCAL_BACKUP_DIR}/$(basename "''${LATEST}")"
              echo "deploy: pulling ''${LATEST} → ''${DEST}…"
              if ${pkgs.openssh}/bin/scp "root@''${HOSTNAME}:''${LATEST}" "''${DEST}"; then
                echo "deploy: local backup saved to ''${DEST}"
                LOCAL_DUMP="''${DEST}"
              else
                # The host copy is the rollback point; the local copy is extra.
                echo "deploy: WARNING — could not pull a local copy; the snapshot is still on the host"
              fi

              # Prune local copies to last 10.
              mapfile -t LOCAL_ALL < <(ls -t "''${LOCAL_BACKUP_DIR}"/brygge-predeploy-*.dump 2>/dev/null || true)
              LOCAL_TOTAL="''${#LOCAL_ALL[@]}"
              if [[ "''${LOCAL_TOTAL}" -gt 10 ]]; then
                echo "deploy: pruning local backup dir (keep=10)…"
                for (( i=10; i<LOCAL_TOTAL; i++ )); do
                  rm -f "''${LOCAL_ALL[''${i}]}"
                  echo "deploy: removed ''${LOCAL_ALL[''${i}]}"
                done
              fi
            else
              echo "deploy: WARNING — SKIP_BACKUP=1: deploying WITHOUT a pre-deploy snapshot"
            fi

            # ── Pending migrations against prod data ─────────────────────
            # After the snapshot, so there is a rollback point. Dry-runs the
            # pending migrations on prod in a transaction that always rolls
            # back, falling back to restoring the dump pulled above when a
            # migration can't be modelled that way. SKIP_PROD_CHECK=1 is the
            # explicit, visible opt-out; PROD_CHECK=snapshot forces the
            # fallback. See scripts/deploy-migration-dryrun.sh.
            check_rc=0
            PROD_PSQL="ssh root@''${HOSTNAME} \"''${PROD_PSQL_REMOTE}\"" \
              PROD_MIGRATION_STATE="''${PROD_MIGRATION_STATE}" \
              SNAPSHOT_DUMP="''${LOCAL_DUMP}" \
              bash "$DRYRUN" || check_rc=$?
            if [[ "''${check_rc}" -ne 0 ]]; then
              echo "deploy: aborting: pending migrations did not pass the prod check (see above)."
              exit "''${check_rc}"
            fi

            ${deploy-rs.packages.${system}.default}/bin/deploy \
              --hostname "$HOSTNAME" \
              "$@" \
              .#brygge
          '');
          meta.description = "Deploy brygge to a running host via deploy-rs (auto-stages untracked source)";
        };

        # Generates a fresh P-256 VAPID key pair for Web Push and prints
        # them as env-var lines ready to paste into /etc/brygge/env.
        # Same keys must persist for the lifetime of the deploy —
        # rotating invalidates every existing browser subscription.
        #
        # VAPID format: public key is the uncompressed P-256 point
        # (0x04 || X || Y, 65 bytes) base64url-encoded; private key is
        # the 32-byte scalar base64url-encoded. Generated via openssl
        # so we don't pull in a node-packages dep.
        apps.gen-vapid = {
          type = "app";
          program = toString (pkgs.writers.writeBash "brygge-gen-vapid" ''
            set -euo pipefail
            tmp=$(mktemp -d)
            trap 'rm -rf "$tmp"' EXIT

            ${pkgs.openssl}/bin/openssl ecparam -name prime256v1 -genkey -noout -out "$tmp/priv.pem"

            # 32-byte private scalar — DER-encoded ECPrivateKey wraps it
            # at a fixed offset for P-256 (7-byte prelude).
            priv=$(${pkgs.openssl}/bin/openssl ec -in "$tmp/priv.pem" -outform DER 2>/dev/null \
              | dd bs=1 skip=7 count=32 2>/dev/null \
              | ${pkgs.coreutils}/bin/base64 -w0 \
              | ${pkgs.coreutils}/bin/tr '+/' '-_' \
              | ${pkgs.coreutils}/bin/tr -d '=')

            # 65-byte uncompressed public point — last 65 bytes of the
            # SubjectPublicKeyInfo DER for a P-256 key.
            pub=$(${pkgs.openssl}/bin/openssl ec -in "$tmp/priv.pem" -pubout -outform DER 2>/dev/null \
              | ${pkgs.coreutils}/bin/tail -c 65 \
              | ${pkgs.coreutils}/bin/base64 -w0 \
              | ${pkgs.coreutils}/bin/tr '+/' '-_' \
              | ${pkgs.coreutils}/bin/tr -d '=')

            cat <<EOF
            # Generated $(${pkgs.coreutils}/bin/date -u +%Y-%m-%dT%H:%M:%SZ) by nix run .#gen-vapid
            # Append to /etc/brygge/env then: systemctl restart brygge
            VAPID_PUBLIC_KEY=$pub
            VAPID_PRIVATE_KEY=$priv
            EOF
          '');
          meta.description = "Generate a fresh P-256 VAPID key pair for Web Push as env-var lines";
        };

        # Provisions a Dendrite "service account" user on a deployed
        # brygge VM and prints the access token in env-var form.
        # Brygge proxies forum reads/writes to Dendrite using this token
        # (see backend/internal/handlers/forum.go:374).
        #
        # Usage: nix run .#gen-dendrite-token -- <vm-host>
        # Example: nix run .#gen-dendrite-token -- 46.225.99.41
        apps.gen-dendrite-token = {
          type = "app";
          program = toString (pkgs.writers.writeBash "brygge-gen-dendrite-token" ''
            set -euo pipefail

            VM="''${1:-}"
            if [[ -z "$VM" ]]; then
              echo "usage: nix run .#gen-dendrite-token -- <vm-host>" >&2
              echo "" >&2
              echo "Creates a 'brygge-svc' service account in the deployed" >&2
              echo "Dendrite and prints DENDRITE_SERVICE_TOKEN to paste into" >&2
              echo "/etc/brygge/env. Re-run to issue a fresh token (revokes" >&2
              echo "the previous one if -r is passed)." >&2
              exit 1
            fi

            USER=brygge-svc
            PW=$(${pkgs.openssl}/bin/openssl rand -base64 24 | tr -d '/+=')

            # Verify create-account is available on the VM. It only is
            # if `pkgs.dendrite` is in environment.systemPackages.
            if ! ${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" "command -v create-account >/dev/null"; then
              echo "ERROR: 'create-account' not found on PATH on $VM." >&2
              echo "Re-deploy with the latest nix/host.nix (dendrite added to systemPackages):" >&2
              echo "  nix run .#deploy -- $VM" >&2
              echo "Then re-run this command." >&2
              exit 1
            fi

            # /etc/dendrite/env (containing REGISTRATION_SHARED_SECRET) is
            # provisioned declaratively by dendrite-secret-init.service
            # on first deploy. Sanity-check that it exists; if not, the
            # deploy hasn't run with the latest nix yet.
            if ! ${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" "test -s /etc/dendrite/env"; then
              echo "ERROR: /etc/dendrite/env missing on $VM." >&2
              echo "Re-deploy with the latest nix/host.nix; dendrite-secret-init.service" >&2
              echo "will provision it on the next activation." >&2
              echo "  nix run .#deploy -- $VM" >&2
              exit 1
            fi

            # NixOS's services.dendrite writes its config under /run
            # (or /nix/store, depending on version) and points the
            # systemd unit at it via --config. Discover the path from
            # the running unit so we don't have to hardcode it.
            CONFIG=$(${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
              "systemctl show dendrite -p ExecStart --value | grep -oE -- '--config [^ ;]+' | head -1 | cut -d' ' -f2")
            if [[ -z "$CONFIG" ]]; then
              echo "ERROR: couldn't discover dendrite config path on $VM." >&2
              echo "Is the dendrite service running?  systemctl status dendrite" >&2
              echo "Raw ExecStart for debugging:" >&2
              ${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" "systemctl show dendrite -p ExecStart --value" >&2
              exit 1
            fi
            echo "    using config: $CONFIG" >&2

            echo "==> creating dendrite user '$USER' on $VM" >&2
            create_output=$(${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
              "create-account -config '$CONFIG' -username $USER -password '$PW' 2>&1" || true)
            if echo "$create_output" | grep -q "already exists"; then
              echo "    (user already exists; resetting password to issue a fresh token)" >&2
              ${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
                "create-account -reset-password -config '$CONFIG' -username $USER -password '$PW'" >&2
            elif echo "$create_output" | grep -q "Created account"; then
              :
            else
              echo "ERROR: create-account failed:" >&2
              echo "$create_output" | sed 's/^/    /' >&2
              exit 1
            fi

            echo "==> logging in to obtain access token" >&2
            TOKEN=$(${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
              "${pkgs.curl}/bin/curl -s -X POST http://127.0.0.1:8008/_matrix/client/v3/login \
                -H 'Content-Type: application/json' \
                -d '{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"$USER\"},\"password\":\"$PW\"}' \
                | ${pkgs.jq}/bin/jq -r .access_token")

            if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
              echo "ERROR: login did not return an access_token. Check Dendrite is reachable" >&2
              echo "at http://127.0.0.1:8008 on $VM and that the homeserver_name in" >&2
              echo "/etc/dendrite/dendrite.yaml matches what create-account used." >&2
              exit 1
            fi

            cat <<EOF
            # Generated $(date -u +%Y-%m-%dT%H:%M:%SZ) by nix run .#gen-dendrite-token
            # Append to /etc/brygge/env on $VM then: systemctl restart brygge
            DENDRITE_SERVICE_TOKEN=$TOKEN
            EOF
          '');
          meta.description = "Provision a brygge service account on a deployed Dendrite and print DENDRITE_SERVICE_TOKEN";
        };
      }
    );
}
