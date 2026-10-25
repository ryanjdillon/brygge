# Fixture check for nix/mail-bounce.nix. A failing case throws at
# evaluation, so `nix flake check --no-build` catches it; the real host
# config can't, because it only ever sees the deployer's local tfvars.
# This tests the pure function only: nix/host.nix must keep
# `assertions = mailBounce.assertions;` wired for a bad spec to fail
# the real evaluation.
{ pkgs }:
let
  domain = "example.invalid";
  bounce = boardMailboxes: import ../mail-bounce.nix { inherit domain boardMailboxes; };
  failing = mbs: builtins.filter (a: !a.assertion) (bounce mbs).assertions;

  post = { address = "post@${domain}"; type = "shared"; };
  styre = { address = "styre@${domain}"; type = "shared"; };
  info = { address = "info@${domain}"; type = "list"; };
  flag = e: e // { receives_bounces = true; };

  cases = {
    "nothing flagged falls back to postmaster" =
      (bounce [ post info ]).bounceAddress == "postmaster@${domain}" && failing [ post info ] == [ ];
    "one flagged shared mailbox receives bounces" =
      (bounce [ (flag post) styre ]).bounceAddress == post.address && failing [ (flag post) styre ] == [ ];
    "type defaults to shared" =
      failing [ (flag { address = post.address; }) ] == [ ];
    "two flagged entries fail" =
      builtins.length (failing [ (flag post) (flag styre) ]) == 1;
    "a flagged list fails" =
      builtins.length (failing [ (flag info) ]) == 1;
    "empty spec falls back to postmaster" =
      (bounce [ ]).bounceAddress == "postmaster@${domain}" && failing [ ] == [ ];
  };

  bad = builtins.filter (name: !cases.${name}) (builtins.attrNames cases);
in
if bad != [ ] then
  throw "nix/mail-bounce.nix fixture failed: ${builtins.concatStringsSep "; " bad}"
else
  pkgs.runCommand "mail-bounce-assertions" { } "touch $out"
