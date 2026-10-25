# Where bounces for the app's own SMTP mail go. The app authenticates as
# the send-only `relay` principal (quota 1), so DSNs addressed to relay@
# fail over quota and Stalwart drops them as double bounces. Stalwart
# rewrites relay's envelope sender to this address instead (host.nix,
# session.mail.rewrite): the one shared board mailbox flagged
# `receives_bounces`, else postmaster@<domain>.
#
# Pure function so the assertions can be exercised against fixtures
# (nix/tests/mail-bounce.nix) without a real tfvars file.
{ domain, boardMailboxes }:
let
  flagged = builtins.filter (e: e.receives_bounces or false) boardMailboxes;
  notShared = builtins.filter (e: (e.type or "shared") != "shared") flagged;
  show = es: builtins.concatStringsSep ", " (map (e: e.address) es);
in
{
  bounceAddress =
    if flagged == [ ] then "postmaster@${domain}" else (builtins.head flagged).address;

  assertions = [
    {
      assertion = builtins.length flagged <= 1;
      message = "board_mailboxes: at most one entry may set receives_bounces = true (found: ${show flagged}).";
    }
    {
      assertion = notShared == [ ];
      message = "board_mailboxes: receives_bounces = true is only valid on a type = \"shared\" mailbox; a list has no mailbox to keep the bounce in (found on: ${show notShared}).";
    }
  ];
}
