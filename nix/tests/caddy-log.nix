# Behavioural check for nix/caddy-log.nix: runs the pinned Caddy with the
# redacting log block on a loopback port, sends requests carrying secrets,
# and fails if any secret reaches the access log. Also runs `caddy validate`
# on the rendered Caddyfile.
# `hostCaddyfile` is the Caddyfile NixOS renders for the real host; it must adapt.
{ pkgs, hostCaddyfile }:
let
  caddyLog = import ../caddy-log.nix { };
in
pkgs.runCommand "brygge-caddy-log-check"
  {
    nativeBuildInputs = [ pkgs.caddy pkgs.curl pkgs.jq ];
  } ''
  export HOME=$TMPDIR XDG_CONFIG_HOME=$TMPDIR XDG_DATA_HOME=$TMPDIR
  logfile=$TMPDIR/access.log
  errfile=$TMPDIR/error.log
  cat > Caddyfile <<'CADDY'
  {
    auto_https off
    admin off
    log {
      output file ERRFILE
  CADDY
  cat >> Caddyfile <<'CADDY'
  ${caddyLog.defaultLogFormat}
  CADDY
  cat >> Caddyfile <<'CADDY'
    }
  }
  http://127.0.0.1:18080 {
    redir /go/pay /betal/SECRET-L/kvittering?token=SECRET-M 302
    redir /go/qr https://h.example/api/v1/qr/SECRET-N?access_token=SECRET-O 302
    handle /fail/* {
      error "boom" 500
    }
    log {
  CADDY
  cat >> Caddyfile <<'CADDY'
  ${builtins.replaceStrings [ "/var/log/caddy/access-HOST.log" ] [ "LOGFILE" ] (caddyLog.logFormat "HOST")}
  CADDY
  sed -i "s#LOGFILE#$logfile#; s#ERRFILE#$errfile#" Caddyfile
  cat >> Caddyfile <<'CADDY'
    }
    respond ok
  }
  CADDY

  caddy validate --config Caddyfile --adapter caddyfile
  # The host's own rendered config must at least adapt (it cannot be run here:
  # it wants /var/log/caddy and public ports).
  caddy adapt --config ${hostCaddyfile} --adapter caddyfile > /dev/null

  caddy run --config Caddyfile --adapter caddyfile >caddy.out 2>&1 &
  pid=$!
  for _ in $(seq 50); do
    curl -s -o /dev/null http://127.0.0.1:18080/ready && break
    sleep 0.2
  done
  : > "$logfile"
  : > "$errfile"

  req() {
    curl -s -o /dev/null \
      -H "Referer: https://h/betal/SECRET-REF?token=SECRET-REFQ" \
      -H "Cookie: s=SECRET-COOKIE" -H "Authorization: Bearer SECRET-AUTH" \
      "http://127.0.0.1:18080$1"
  }
  req "/api/v1/auth/verify?token=SECRET-A&x=1"
  req "/login/verify?token=SECRET-L"
  req "/api/v1/unsubscribe?a=1&token=SECRET-B"
  req "/join/confirm?token=SECRET-C"
  req "/api/v1/join/confirm/SECRET-D"
  req "/betal/SECRET-E"
  req "/betal/bruker/SECRET-F"
  req "/api/v1/betal/SECRET-G/kvittering"
  req "/api/v1/checkout/SECRET-H/pay"
  req "/qr/SECRET-I/pay"
  req "/_matrix/client/v3/sync?access_token=SECRET-P"
  req "/go/pay"
  req "/go/qr"
  # A failed request is written to the global error log with its URI.
  req "/fail/betal/SECRET-J?token=SECRET-K"
  sleep 1
  kill $pid || true

  for f in "$logfile" "$errfile"; do
    if grep -q SECRET "$f"; then
      echo "secret reached $f:" >&2
      grep SECRET "$f" >&2
      exit 1
    fi
  done
  grep -q 'boom' "$errfile" || { echo "error log is missing the failed request" >&2; exit 1; }
  [ "$(wc -l < "$logfile")" -ge 13 ] || { echo "access log is missing requests" >&2; exit 1; }
  # Non-secret parts of the URI must survive, or the log is useless.
  grep -q '/api/v1/auth/verify?token=REDACTED&x=1' "$logfile"
  grep -q '/api/v1/checkout/REDACTED/pay' "$logfile"
  # Redirects: the Location header is redacted and still present.
  grep -q '/betal/REDACTED/kvittering?token=REDACTED' "$logfile"
  grep -q '/api/v1/qr/REDACTED?access_token=REDACTED' "$logfile"
  touch $out
''
