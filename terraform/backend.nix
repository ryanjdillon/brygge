{ ... }:
{
  terraform.backend.s3 = {
    bucket = "brygge-tfstate";
    key    = "brygge/terraform.tfstate";
    region = "eu-central-1";

    # Hetzner Object Storage (S3-compatible)
    # Credentials passed via AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY env vars
    endpoints.s3                    = "https://nbg1.your-objectstorage.com";
    skip_credentials_validation     = true;
    skip_metadata_api_check         = true;
    skip_region_validation          = true;
    skip_requesting_account_id      = true;
    skip_s3_checksum                = true;
  };
}
