{
  description = "Brygge — harbor club platform";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    terranix.url = "github:terranix/terranix";
    terranix.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, flake-utils, terranix }:
    let
      # Terranix configuration (system-independent definition)
      mkTerraformConfig = system: terranix.lib.terranixConfiguration {
        inherit system;
        modules = [ ./terraform/config.nix ];
      };
    in
    flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" ] (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        terraformConfiguration = mkTerraformConfig system;
      in
      {
        packages.terraformConfiguration = terraformConfiguration;

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Go toolchain
            go
            golangci-lint

            # Node.js
            nodejs_22

            # Database tooling
            sqlc
            go-migrate  # golang-migrate CLI
            postgresql_16   # provides psql client

            # atlas (ariga.io/atlas) is not currently packaged in nixpkgs.
            # Install manually: https://atlasgo.io/getting-started#installation
            # curl -sSf https://atlasgo.sh | sh

            # Cache
            redis  # provides redis-cli

            # Playwright: install browsers via npm after entering the shell.
            # Run: npx playwright install
            # playwright-driver is available in nixpkgs but bundles Chromium only;
            # use npm-based install for full browser support.

            # Containers
            docker-compose

            # Infrastructure
            opentofu

            # Task runner
            just

            # General utilities
            git
            curl
            jq
          ];

          shellHook = ''
            echo "brygge dev shell ready"

            # Set GOPATH to a local directory so module cache stays in the project tree
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"

            # Make locally installed npm binaries available
            export PATH="$PWD/node_modules/.bin:$PATH"

            # Point Playwright to its browser cache when the nixpkgs driver is present
            if [ -n "''${PLAYWRIGHT_DRIVER_PATH:-}" ]; then
              export PLAYWRIGHT_BROWSERS_PATH="''${PLAYWRIGHT_DRIVER_PATH}"
            fi
          '';
        };

        # Terraform plan via Terranix
        apps.tf-plan = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-plan" ''
            set -euo pipefail
            cd "$(${pkgs.git}/bin/git rev-parse --show-toplevel)/terraform"
            [[ -f terraform.tfvars ]] || { echo "error: terraform/terraform.tfvars not found (copy from terraform.tfvars.example)"; exit 1; }
            export AWS_ACCESS_KEY_ID=$(${pkgs.gnugrep}/bin/grep -oP 'hetzner_s3_access_key\s*=\s*"\K[^"]+' terraform.tfvars)
            export AWS_SECRET_ACCESS_KEY=$(${pkgs.gnugrep}/bin/grep -oP 'hetzner_s3_secret_key\s*=\s*"\K[^"]+' terraform.tfvars)
            if [[ -e config.tf.json ]]; then rm -f config.tf.json; fi
            cp ${terraformConfiguration} config.tf.json
            echo "backend bucket: $(${pkgs.jq}/bin/jq -r '.terraform.backend.s3.bucket' config.tf.json)"
            echo "backend endpoint: $(${pkgs.jq}/bin/jq -r '.terraform.backend.s3.endpoints.s3' config.tf.json)"
            echo "AWS_ACCESS_KEY_ID is set: $([ -n "$AWS_ACCESS_KEY_ID" ] && echo yes || echo no)"
            echo "testing S3 access..."
            ${pkgs.curl}/bin/curl -s -o /dev/null -w "S3 HEAD bucket: HTTP %{http_code}\n" \
              --head "https://brygge-tfstate.fsn1.your-objectstorage.com/" \
              --aws-sigv4 "aws:amz:eu-central-1:s3" \
              --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY"
            ${pkgs.opentofu}/bin/tofu init
            ${pkgs.opentofu}/bin/tofu plan "$@"
          '');
        };

        # Terraform apply via Terranix
        apps.tf-apply = {
          type = "app";
          program = toString (pkgs.writers.writeBash "tf-apply" ''
            set -euo pipefail
            cd "$(${pkgs.git}/bin/git rev-parse --show-toplevel)/terraform"
            [[ -f terraform.tfvars ]] || { echo "error: terraform/terraform.tfvars not found (copy from terraform.tfvars.example)"; exit 1; }
            export AWS_ACCESS_KEY_ID=$(${pkgs.gnugrep}/bin/grep -oP 'hetzner_s3_access_key\s*=\s*"\K[^"]+' terraform.tfvars)
            export AWS_SECRET_ACCESS_KEY=$(${pkgs.gnugrep}/bin/grep -oP 'hetzner_s3_secret_key\s*=\s*"\K[^"]+' terraform.tfvars)
            if [[ -e config.tf.json ]]; then rm -f config.tf.json; fi
            cp ${terraformConfiguration} config.tf.json
            ${pkgs.opentofu}/bin/tofu init
            ${pkgs.opentofu}/bin/tofu apply "$@"
          '');
        };
      });
}
