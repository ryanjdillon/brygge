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

  output.domain = {
    value       = "\${var.domain}";
    description = "Primary domain for the club";
  };

  output.dns_zone_id = {
    value       = "\${hcloud_zone.main.id}";
    description = "Hetzner DNS zone ID";
  };

  # Null when SES is disabled. Informational: nix/host.nix derives the
  # same names from clubConfig.slug rather than consuming these.
  output.ses_configuration_set = {
    value       = "\${one(aws_sesv2_configuration_set.main[*].configuration_set_name)}";
    description = "SES configuration set Brygge names in X-SES-CONFIGURATION-SET";
  };

  output.ses_events_queue = {
    value       = "\${one(aws_sqs_queue.ses_events[*].name)}";
    description = "SQS queue carrying SES delivery events to Brygge";
  };

  output.ses_events_dlq = {
    value       = "\${one(aws_sqs_queue.ses_events_dlq[*].name)}";
    description = "Dead-letter queue for SES delivery events Brygge failed to process";
  };

  output.ses_app_iam_user = {
    value       = "\${one(aws_iam_user.app[*].name)}";
    description = "IAM user whose access key (created by hand) Brygge runs as";
  };
}
