{
  description = "Brygge development shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" ] (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
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
      });
}
