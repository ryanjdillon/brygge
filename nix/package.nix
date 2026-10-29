{
  lib,
  buildGoModule,
  buildNpmPackage,
  nodejs_22,
}:

let
  repoRoot = lib.cleanSource ../.;

  frontend = buildNpmPackage {
    pname = "brygge-frontend";
    version = "0.1.0";

    src = lib.cleanSource ../frontend;

    nodejs = nodejs_22;

    npmDepsHash = "sha256-sJ0bKq7ZTvAyyUp35HUQOilFDkzFPeQqjeYiFdf7MnY=";

    installPhase = ''
      runHook preInstall
      mkdir -p $out
      cp -r dist/. $out/
      runHook postInstall
    '';
  };

  # Shared Go build attributes for every brygge binary.
  goCommon = {
    version = "0.1.0";
    src = repoRoot;
    modRoot = "backend";
    vendorHash = null;
    ldflags = [
      "-s"
      "-w"
    ];
    env.CGO_ENABLED = "0";
    # The embed target must exist even for binaries that never serve the
    # SPA, so the //go:embed directive resolves whenever internal/web is
    # pulled into the build graph.
    preBuild = ''
      mkdir -p internal/web/dist
      cp -r ${frontend}/. internal/web/dist/
    '';
  };

  # Operational one-shot tools (seed, data backfills, cutovers). Built as
  # a separate package so they stay OUT of the deployed system closure —
  # none run as a service, so shipping them to the server on every deploy
  # only inflated the pushed closure. Run them on demand instead:
  #   nix run .#seed
  #   nix run .#vipps-cutover
  tools = buildGoModule (
    goCommon
    // {
      pname = "brygge-tools";
      subPackages = [
        "cmd/seed"
        "cmd/vipps-cutover"
        "cmd/vipps-audit"
        "cmd/vipps-webhook-register"
        "cmd/vipps-report"
      ];
      postInstall = ''
        mv $out/bin/seed $out/bin/brygge-seed
      '';
    }
  );
in
buildGoModule (
  goCommon
  // {
    pname = "brygge";

    subPackages = [ "cmd/api" ];

    postInstall = ''
      mv $out/bin/api $out/bin/brygge
    '';

    passthru = {
      inherit frontend tools;
      migrations = "${repoRoot}/backend/migrations";
    };

    meta = with lib; {
      description = "Brygge — harbor club platform (Go API + embedded Vue SPA)";
      homepage = "https://github.com/brygge-klubb/brygge";
      license = licenses.mit;
      mainProgram = "brygge";
      platforms = platforms.linux;
    };
  }
)
