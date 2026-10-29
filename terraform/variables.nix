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
    s3_bucket_docs = {
      type        = "string";
      description = "S3 bucket name for document uploads (used by the brygge app as S3_BUCKET_DOCS).";
    };
    s3_bucket_backups = {
      type        = "string";
      description = "S3 bucket name for automated database backups.";
    };
    s3_bucket_legal = {
      type        = "string";
      description = "S3 bucket name for legal/financial documents (invoices, receipts). Norwegian bokføringsloven §13 requires 5-year retention.";
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

    # Read only by the NixOS flake; declared so Terraform doesn't warn about
    # an undeclared variable in the shared tfvars file.
    tailnet_domain = {
      type        = "string";
      default     = "";
      description = "Tailnet DNS suffix (MagicDNS) the host's resolver forwards to 100.100.100.100, so the OTel collector's upstream endpoint resolves. Empty disables the forward.";
    };

    # Self-hosted mail (Stalwart)
    dkim_public_value = {
      type        = "string";
      default     = "";
      description = "DKIM public key TXT record value for selector `mail._domainkey`. Empty until first boot of simple-nixos-mailserver has generated the key at /var/dkim/<domain>.mail.txt — fetch it then paste and tf-apply again.";
    };
    dmarc_policy = {
      type        = "string";
      default     = "quarantine";
      description = "DMARC policy: none | quarantine | reject. Start with quarantine; tighten to reject once deliverability is proven.";
    };

    # Stripe custom email domain: Stripe sends receipts from the club's domain.
    stripe_email_verification = {
      type        = "string";
      default     = "";
      description = "Full `stripe-verification=…` value from Stripe's \"Connect your domain\" dialog, published as an apex TXT record next to SPF. Empty publishes nothing.";
      validation = {
        condition     = "\${var.stripe_email_verification == \"\" || can(regex(\"^stripe-verification=[A-Za-z0-9]+$\", var.stripe_email_verification))}";
        error_message = "stripe_email_verification must be the bare stripe-verification=<value> string, without quotes.";
      };
    };
    stripe_email_dkim_tokens = {
      type        = "list(string)";
      default     = [ ];
      description = "The DKIM selector tokens from Stripe's \"Connect your domain\" dialog (the part before `._domainkey`). Each is published as `<token>._domainkey` CNAME → `<token>.dkim.custom-email-domain.stripe.com.`. Setting either Stripe email variable also publishes the `bounce` CNAME.";
      validation = {
        condition     = "\${alltrue([for t in var.stripe_email_dkim_tokens : can(regex(\"^[a-z0-9]+$\", t))])}";
        error_message = "stripe_email_dkim_tokens must hold only the token, without the ._domainkey suffix.";
      };
    };

    # Shared role-mapped board mailboxes (DIL-275/276). Reader ACLs are
    # reconciled from `user_roles` by the Brygge backend; this variable
    # only declares the principals and role mapping.
    board_mailboxes = {
      type = "list(object({"
        + "address = string,"
        + "role = optional(string, \"\"),"
        + "display_name = string,"
        + "type = string,"
        + "send_as = optional(bool, false),"
        + "bcc_members = optional(bool, false),"
        + "from_brand_only = optional(bool, false),"
        + "receives_bounces = optional(bool, false),"
        + "retention_days = optional(number),"
        + "managed = optional(bool, true)"
        + "}))";
      default     = [];
      description = "Role-mapped board mailboxes (e.g. kasserar@, styre@). `type=shared` participates in role-gated ACL reconciliation; `type=list` is a pure alias. At most one `type=shared` entry may set `receives_bounces` to receive bounces for the app's mail (default postmaster@).";
    };
  };
}
