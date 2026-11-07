# Per-vhost Caddy access-log block with bearer secrets redacted.
#
# Several URLs carry a bearer secret in the request line (magic-link and
# unsubscribe `?token=`, `/betal/{token}`, `/checkout/{token}`, `/qr/{token}`,
# `/join/confirm`). Caddy's access log records the full URI and, for requests
# a page made, the Referer, which holds the page's own URL; a redirect's
# Location holds the destination (/betal/{token}, /qr/{token}). All three are
# redacted before they reach the log. Authorization, Cookie and Set-Cookie are
# redacted by Caddy itself unless `log_credentials` is set; the fixture check
# in nix/tests/caddy-log.nix asserts that stays true.
#
# Keep `pathTokenRoutes` in step with the token-addressed routes in
# backend/cmd/api/routes.go and frontend/src/router/index.ts.
{ }:
let
  # Query parameters that hold a secret or a one-shot challenge id
  # (access_token is the Matrix client API's).
  queryParams = [ "token" "cid" "access_token" ];

  # Path prefixes followed by a token segment, with or without /api/v1.
  # `betal/bruker` comes before `betal` so its token is the one redacted.
  pathTokenRoutes = [ "betal/bruker" "betal" "checkout" "qr" "join/confirm" ];

  alt = builtins.concatStringsSep "|";

  queryRegexp = "([?&](?:${alt queryParams})=)[^&]*";
  pathRegexp = "(/(?:api/v1/)?(?:${alt pathTokenRoutes})/)[^/?&#]+";

  # Caddyfile `${1}` is a regexp group reference; the Nix string below
  # escapes it as ''${1}.
  rules = ''
    regexp "${queryRegexp}" "''${1}REDACTED"
    regexp "${pathRegexp}" "''${1}REDACTED"
  '';
  filterBlock = ''
    format filter {
      wrap json
      request>uri multi_regexp {
        ${rules}
      }
      request>headers>Referer multi_regexp {
        ${rules}
      }
      resp_headers>Location multi_regexp {
        ${rules}
      }
    }
  '';

  # Body of the global `log` (services.caddy.logFormat). Caddy writes error
  # entries for a failed request, request URI included, to this log on stderr,
  # which journald holds and the OTel collector ships off-host.
  defaultLogFormat = ''
    level ERROR
    ${filterBlock}
  '';
in
{
  inherit queryRegexp pathRegexp defaultLogFormat;

  # `logFormat` body for services.caddy.virtualHosts.<name>; `host` names the
  # file the way the NixOS module does by default.
  logFormat = host: ''
    output file /var/log/caddy/access-${host}.log
    ${filterBlock}
  '';
}
