{ ... }:
{
  resource.hcloud_ssh_key.deploy = {
    name       = "\${var.server_name}-deploy";
    public_key = "\${var.ssh_public_key}";
  };

  resource.hcloud_server.brygge = {
    name        = "\${var.server_name}";
    server_type = "\${var.server_type}";
    image       = "\${var.image}";
    location    = "\${var.location}";
    ssh_keys    = [ "\${hcloud_ssh_key.deploy.id}" ];
    labels = {
      app        = "brygge";
      managed-by = "terraform";
    };
    user_data = "\${file(\"cloud-init.yml\")}";
  };

  resource.hcloud_firewall.brygge = {
    name = "\${var.server_name}-firewall";
    rule = [
      {
        direction   = "in";
        protocol    = "tcp";
        port        = "22";
        source_ips  = [ "0.0.0.0/0" "::/0" ];
        description = "SSH";
      }
      {
        direction   = "in";
        protocol    = "tcp";
        port        = "80";
        source_ips  = [ "0.0.0.0/0" "::/0" ];
        description = "HTTP";
      }
      {
        direction   = "in";
        protocol    = "tcp";
        port        = "443";
        source_ips  = [ "0.0.0.0/0" "::/0" ];
        description = "HTTPS";
      }
    ];
  };

  resource.hcloud_firewall_attachment.brygge = {
    firewall_id = "\${hcloud_firewall.brygge.id}";
    server_ids  = [ "\${hcloud_server.brygge.id}" ];
  };
}
