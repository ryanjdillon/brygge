{
  description = "Brygge — harbor club platform";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

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
        specialArgs = { inherit self clubConfig; };
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

      checks = builtins.mapAttrs (_: lib: lib.deployChecks self.deploy) deploy-rs.lib;
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
            opentofu
            hcloud
            just
            git
            curl
            jq
            deploy-rs.packages.${system}.default
          ];

          shellHook = ''
            echo "brygge dev shell ready"
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

        apps.tf-plan = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-plan" ''
            set -euo pipefail
            ${tfInit}
            ${pkgs.opentofu}/bin/tofu plan "$@"
          '');
        };

        apps.tf-apply = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-apply" ''
            set -euo pipefail
            ${tfInit}
            ${pkgs.opentofu}/bin/tofu apply "$@"
          '');
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

            ${deploy-rs.packages.${system}.default}/bin/deploy \
              --hostname "$HOSTNAME" \
              "$@" \
              .#brygge
          '');
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

            echo "==> creating dendrite user '$USER' on $VM" >&2
            ${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
              "create-account -config /etc/dendrite/dendrite.yaml -username $USER -password '$PW' 2>&1 | tail -5" \
              || echo "(ignored — user likely already exists; will log in to refresh token)" >&2

            echo "==> logging in to obtain access token" >&2
            TOKEN=$(${pkgs.openssh}/bin/ssh -o BatchMode=yes "root@$VM" \
              "${pkgs.curl}/bin/curl -s -X POST http://127.0.0.1:8008/_matrix/client/v3/login \
                -H 'Content-Type: application/json' \
                -d '{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"$USER\"},\"password\":\"$PW\"}' \
                | ${pkgs.jq}/bin/jq -r .access_token")

            if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
              echo "ERROR: login did not return an access_token. Most likely the password" >&2
              echo "differs from a previous run. Either:" >&2
              echo "  1. Reset it: ssh root@$VM 'create-account -reset-password -config /etc/dendrite/dendrite.yaml -username $USER -password <new>'" >&2
              echo "  2. Use the password from a previous run if you saved it." >&2
              exit 1
            fi

            cat <<EOF
            # Generated $(date -u +%Y-%m-%dT%H:%M:%SZ) by nix run .#gen-dendrite-token
            # Append to /etc/brygge/env on $VM then: systemctl restart brygge
            DENDRITE_SERVICE_TOKEN=$TOKEN
            EOF
          '');
        };
      }
    );
}
