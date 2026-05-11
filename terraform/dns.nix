{ ... }:
{
  # DNS zone managed via Hetzner Cloud API
  resource.hcloud_zone.main = {
    name = "\${var.domain}";
    mode = "primary";
    ttl  = 3600;
  };

  # A records — all point to the brygge server's IPv4 address
  resource.hcloud_zone_rrset = {
    root = {
      zone    = "\${var.domain}";
      name    = "@";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };
    matrix = {
      zone    = "\${var.domain}";
      name    = "matrix";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };
    element = {
      zone    = "\${var.domain}";
      name    = "element";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };
    status = {
      zone    = "\${var.domain}";
      name    = "status";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };

    # Self-hosted mail (simple-nixos-mailserver)
    mail_a = {
      zone    = "\${var.domain}";
      name    = "mail";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };
    # AAAA intentionally omitted until IPv6 is configured on the host
    # interface. Hetzner allocates a /64 but the server currently has only
    # link-local IPv6 — no global address on enp1s0 under NixOS default
    # DHCP. Revisit once networking.interfaces.enp1s0.ipv6 is set up.
    webmail = {
      zone    = "\${var.domain}";
      name    = "webmail";
      type    = "A";
      ttl     = 300;
      records = [{ value = "\${hcloud_server.brygge.ipv4_address}"; }];
    };
    root_mx = {
      zone    = "\${var.domain}";
      name    = "@";
      type    = "MX";
      ttl     = 300;
      records = [{ value = "10 mail.\${var.domain}."; }];
    };
    root_spf = {
      zone    = "\${var.domain}";
      name    = "@";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\"v=spf1 mx -all\""; }];
    };
    mail_dkim = {
      count   = "\${var.dkim_public_value != \"\" ? 1 : 0}";
      zone    = "\${var.domain}";
      name    = "mail._domainkey";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\${var.dkim_public_value}"; }];
    };
    dmarc = {
      zone    = "\${var.domain}";
      name    = "_dmarc";
      type    = "TXT";
      ttl     = 300;
      # rua must be on the sending domain so receivers don't have to
      # publish a cross-domain authorization (`_report._dmarc` TXT) on
      # their side. `postmaster@<domain>` is the RFC 5321 well-known
      # mailbox; provision it as a forwarder in Stalwart so reports
      # reach the operator without surfacing the human address in DNS.
      records = [{ value = "\"v=DMARC1; p=\${var.dmarc_policy}; rua=mailto:postmaster@\${var.domain}; fo=1\""; }];
    };

    # BIMI — points receiving MTAs at the SVG to display next to
    # outbound mail. The SVG is served by the brygge API at
    # /api/v1/club/logo (the same site logo used in the navbar). Two
    # caveats worth noting:
    #
    # 1. The SVG must comply with BIMI's SVG Tiny 1.2 Portable/Secure
    #    profile (square viewBox, no scripts, no external refs,
    #    ≤32 kB). Validate with https://bimigroup.org/svg-validator/
    #    before publishing — receivers will reject a non-compliant
    #    SVG and your logo just won't render.
    #
    # 2. Gmail requires a Verified Mark Certificate (VMC) for actual
    #    inbox rendering — `a=` parameter would point at a .pem file
    #    if you ever buy one (~$1500/year, requires registered
    #    trademark). Yahoo / Apple Mail / Fastmail render without
    #    VMC; the record below is enough for them.
    bimi = {
      zone    = "\${var.domain}";
      name    = "default._bimi";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\"v=BIMI1; l=https://\${var.domain}/api/v1/club/logo.svg\""; }];
    };
    autoconfig = {
      zone    = "\${var.domain}";
      name    = "autoconfig";
      type    = "CNAME";
      ttl     = 300;
      records = [{ value = "mail.\${var.domain}."; }];
    };
    imaps_srv = {
      zone    = "\${var.domain}";
      name    = "_imaps._tcp";
      type    = "SRV";
      ttl     = 300;
      records = [{ value = "0 0 993 mail.\${var.domain}."; }];
    };
    submission_srv = {
      zone    = "\${var.domain}";
      name    = "_submission._tcp";
      type    = "SRV";
      ttl     = 300;
      records = [{ value = "0 0 587 mail.\${var.domain}."; }];
    };

  };
}
