{ ... }:
{
  resource.hcloud_ssh_key.admin = {
    for_each   = "\${toset(var.admin_ssh_keys)}";
    name       = "\${var.server_name}-\${substr(sha256(each.value), 0, 8)}";
    public_key = "\${each.value}";
  };

  resource.hcloud_server.brygge = {
    name        = "\${var.server_name}";
    server_type = "\${var.server_type}";
    image       = "\${var.image}";
    location    = "\${var.location}";
    ssh_keys    = "\${[for k in hcloud_ssh_key.admin : k.id]}";
    labels = {
      app        = "brygge";
      managed-by = "terraform";
    };
    # NixOS is installed post-boot via nixos-anywhere (nix run .#install).
    # Enable rescue mode before running the installer:
    #   hcloud server enable-rescue <server_name> --type linux64
    #   hcloud server reset <server_name>
  };

  resource.hcloud_firewall.brygge = {
    name = "\${var.server_name}-firewall";
    rule = [
      { direction = "in"; protocol = "tcp"; port = "22";  source_ips = [ "0.0.0.0/0" "::/0" ]; description = "SSH";        }
      { direction = "in"; protocol = "tcp"; port = "80";  source_ips = [ "0.0.0.0/0" "::/0" ]; description = "HTTP";       }
      { direction = "in"; protocol = "tcp"; port = "443"; source_ips = [ "0.0.0.0/0" "::/0" ]; description = "HTTPS";      }
      # Mail server (Stalwart). The host firewall already opens these
      # in nix/host.nix, but Hetzner's cloud firewall sits in front and
      # silently drops un-listed ports — so external MTAs (Gmail etc.)
      # get a TCP timeout when delivering to mail.<domain>:25, mail
      # piles up in their deferred queue, and no bounce comes back for
      # ~3 days. Mirror the host firewall here.
      { direction = "in"; protocol = "tcp"; port = "25";  source_ips = [ "0.0.0.0/0" "::/0" ]; description = "SMTP";       }
      { direction = "in"; protocol = "tcp"; port = "465"; source_ips = [ "0.0.0.0/0" "::/0" ]; description = "SMTPS";      }
      { direction = "in"; protocol = "tcp"; port = "587"; source_ips = [ "0.0.0.0/0" "::/0" ]; description = "Submission"; }
      { direction = "in"; protocol = "tcp"; port = "993"; source_ips = [ "0.0.0.0/0" "::/0" ]; description = "IMAPS";      }
    ];
  };

  resource.hcloud_firewall_attachment.brygge = {
    firewall_id = "\${hcloud_firewall.brygge.id}";
    server_ids  = [ "\${hcloud_server.brygge.id}" ];
  };

  # Reverse DNS for mail deliverability. Gmail/Outlook require PTR to match
  # the HELO hostname (FCrDNS). Points at mail.<domain>; ensure dns.nix
  # publishes an A record for `mail` in the same tf-apply.
  resource.hcloud_rdns.brygge_ipv4 = {
    server_id  = "\${hcloud_server.brygge.id}";
    ip_address = "\${hcloud_server.brygge.ipv4_address}";
    dns_ptr    = "mail.\${var.domain}";
  };
  # IPv6 rDNS omitted until the host gets a global IPv6 on enp1s0.
}
