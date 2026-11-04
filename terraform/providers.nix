{ ... }:
{
  terraform.required_providers = {
    hcloud = {
      source  = "hetznercloud/hcloud";
      version = "~> 1.56";
    };
    aws = {
      source  = "hashicorp/aws";
      version = "~> 5.0";
    };
  };

  provider.hcloud.token = "\${var.hcloud_token}";

  # The tf wrappers export AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY for the
  # Hetzner S3 state backend. An empty access_key would make the provider
  # fall back to that ambient chain and act on AWS with Hetzner
  # credentials, so the keys are always explicit: the tfvars keys when
  # set, otherwise a placeholder with every startup AWS call skipped.
  # The placeholder lets plan run with SES disabled and no AWS account;
  # aws/ses.nix fails the plan early if SES is enabled without keys.
  locals.aws_keys_set = "\${var.aws_access_key != \"\" && var.aws_secret_key != \"\"}";

  provider.aws = {
    region                      = "\${var.aws_region}";
    access_key                  = "\${local.aws_keys_set ? var.aws_access_key : \"unset\"}";
    secret_key                  = "\${local.aws_keys_set ? var.aws_secret_key : \"unset\"}";
    skip_credentials_validation = "\${!local.aws_keys_set}";
    skip_requesting_account_id  = "\${!local.aws_keys_set}";
    # Never consult EC2 instance metadata for credentials or region.
    skip_metadata_api_check     = true;
    default_tags.tags = {
      app        = "brygge";
      club       = "\${var.club_slug}";
      managed-by = "terraform";
    };
  };
}
