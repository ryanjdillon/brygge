{ ... }:
{
  variable = {
    hcloud_token = {
      type      = "string";
      sensitive = true;
      description = "Hetzner Cloud API token (dual-purpose: cloud + DNS).";
    };

    admin_email = {
      type        = "string";
      description = "Admin email. Used as ACME contact for Let's Encrypt.";
    };

    admin_ssh_keys = {
      type        = "list(string)";
      description = "SSH public keys authorized as root on the NixOS host and attached to the Hetzner project for rescue-mode bootstrap.";
    };

    hetzner_s3_access_key = {
      type        = "string";
      sensitive   = true;
      description = "Hetzner Object Storage access key (used for Terraform state backend).";
    };
    hetzner_s3_secret_key = {
      type        = "string";
      sensitive   = true;
      description = "Hetzner Object Storage secret key.";
    };
    s3_bucket = {
      type        = "string";
      description = "S3 bucket name for Terraform state.";
    };
    s3_endpoint = {
      type        = "string";
      description = "S3-compatible endpoint URL (e.g. https://nbg1.your-objectstorage.com).";
    };

    domain = {
      type        = "string";
      description = "Primary domain for the club (e.g. klubb.no).";
    };

    server_name = {
      type        = "string";
      default     = "brygge";
      description = "Hetzner server name and NixOS hostname.";
    };
    server_type = {
      type        = "string";
      default     = "cx23";
      description = "Hetzner server type (cx23 = x86_64, 2 vCPU, 4 GB RAM, 40 GB disk).";
    };
    location = {
      type        = "string";
      default     = "nbg1";
      description = "Hetzner datacenter (fsn1=Falkenstein, nbg1=Nuremberg, hel1=Helsinki).";
    };
    image = {
      type        = "string";
      default     = "debian-12";
      description = "Initial image (replaced by NixOS via nixos-anywhere post-bootstrap).";
    };
    timezone = {
      type        = "string";
      default     = "Europe/Oslo";
      description = "IANA timezone for the host (systemd time.timeZone).";
    };

    # Resend email DNS (optional — leave empty to skip)
    resend_dkim_value = {
      type        = "string";
      default     = "";
      description = "Resend DKIM public key TXT record value.";
    };
    resend_spf_value = {
      type        = "string";
      default     = "";
      description = "Resend SPF TXT record value for send subdomain.";
    };
    resend_mx_value = {
      type        = "string";
      default     = "";
      description = "Resend MX value for send subdomain.";
    };
  };
}
