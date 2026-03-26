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

    # Email (Resend) — only created when resend_dkim_value is set
    resend_dkim = {
      count   = "\${var.resend_dkim_value != \"\" ? 1 : 0}";
      zone    = "\${var.domain}";
      name    = "resend._domainkey";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\${var.resend_dkim_value}"; }];
    };
    resend_spf = {
      count   = "\${var.resend_spf_value != \"\" ? 1 : 0}";
      zone    = "\${var.domain}";
      name    = "send";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\${var.resend_spf_value}"; }];
    };
    resend_mx = {
      count   = "\${var.resend_mx_value != \"\" ? 1 : 0}";
      zone    = "\${var.domain}";
      name    = "send";
      type    = "MX";
      ttl     = 300;
      records = [{ value = "10 \${var.resend_mx_value}."; }];
    };
    resend_dmarc = {
      count   = "\${var.resend_dkim_value != \"\" ? 1 : 0}";
      zone    = "\${var.domain}";
      name    = "_dmarc";
      type    = "TXT";
      ttl     = 300;
      records = [{ value = "\"v=DMARC1; p=none;\""; }];
    };
  };
}
