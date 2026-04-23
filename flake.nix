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
    # values, so the flake can read it in pure eval. Each deployer edits
    # it with their real values locally and runs
    #   git update-index --skip-worktree terraform/terraform.tfvars.json
    # to prevent those changes from being staged or committed.
    tfvars = builtins.fromJSON (builtins.readFile ./terraform/terraform.tfvars.json);

    clubConfig = {
      domain       = tfvars.domain;
      adminEmail   = tfvars.admin_email;
      adminSshKeys = tfvars.admin_ssh_keys;
      hostname     = tfvars.server_name;
      timezone     = tfvars.timezone;
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

            # Install pre-commit hook that blocks committing terraform.tfvars.json.
            # If you haven't yet marked the file skip-worktree, run:
            #   git update-index --skip-worktree terraform/terraform.tfvars.json
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
      }
    );
}
