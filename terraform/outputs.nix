{ ... }:
{
  output.server_ipv4 = {
    value       = "\${hcloud_server.brygge.ipv4_address}";
    description = "Public IPv4 address of the brygge server";
  };

  output.server_ipv6 = {
    value       = "\${hcloud_server.brygge.ipv6_address}";
    description = "Public IPv6 network of the brygge server";
  };
}
