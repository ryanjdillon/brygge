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

    npmDepsHash = "sha256-9e/UOW5hzkMMMcwAgShIUcQoRvGgEYHmuFbK9VIOiUo=";

    installPhase = ''
      runHook preInstall
      mkdir -p $out
      cp -r dist/. $out/
      runHook postInstall
    '';
  };
in
buildGoModule rec {
  pname = "brygge";
  version = "0.1.0";

  src = repoRoot;
  modRoot = "backend";

  vendorHash = "sha256-VGgl6p/KUJmYtotLSRu3XaHfzHURbTpdwsAsPoomRAo=";

  subPackages = [
    "cmd/api"
    "cmd/seed"
  ];

  ldflags = [
    "-s"
    "-w"
  ];

  env.CGO_ENABLED = "0";

  preBuild = ''
    mkdir -p internal/web/dist
    cp -r ${frontend}/. internal/web/dist/
  '';

  postInstall = ''
    mv $out/bin/api $out/bin/brygge
    mv $out/bin/seed $out/bin/brygge-seed
  '';

  passthru = {
    inherit frontend;
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
