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
      { direction = "in"; protocol = "tcp"; port = "22";  source_ips = [ "0.0.0.0/0" "::/0" ]; description = "SSH";   }
      { direction = "in"; protocol = "tcp"; port = "80";  source_ips = [ "0.0.0.0/0" "::/0" ]; description = "HTTP";  }
      { direction = "in"; protocol = "tcp"; port = "443"; source_ips = [ "0.0.0.0/0" "::/0" ]; description = "HTTPS"; }
    ];
  };

  resource.hcloud_firewall_attachment.brygge = {
    firewall_id = "\${hcloud_firewall.brygge.id}";
    server_ids  = [ "\${hcloud_server.brygge.id}" ];
  };
}
