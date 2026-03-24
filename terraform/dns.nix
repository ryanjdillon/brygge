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
  };
}
