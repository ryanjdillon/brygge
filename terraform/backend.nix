{ ... }:
{
  # Backend is configured dynamically via -backend-config flags in tf-plan/tf-apply.
  # The bucket, endpoint, and credentials are read from terraform.tfvars by the nix wrapper.
  # Only static compatibility flags are set here.
  terraform.backend.s3 = {
    key    = "brygge/terraform.tfstate";
    region = "eu-central-1";

    # Hetzner Object Storage (S3-compatible) — skip AWS-specific checks
    skip_credentials_validation = true;
    skip_metadata_api_check     = true;
    skip_region_validation      = true;
    skip_requesting_account_id  = true;
    skip_s3_checksum            = true;
  };
}
