{ ... }:
{
  variable = {
    hcloud_token = {
      type      = "string";
      sensitive = true;
    };
    ssh_public_key = {
      type        = "string";
      description = "SSH public key for server access";
    };
    hetzner_s3_access_key = {
      type        = "string";
      sensitive   = true;
      description = "Hetzner Object Storage access key (used by backend via env vars)";
    };
    hetzner_s3_secret_key = {
      type        = "string";
      sensitive   = true;
      description = "Hetzner Object Storage secret key (used by backend via env vars)";
    };
    server_name = {
      type    = "string";
      default = "brygge";
    };
    server_type = {
      type        = "string";
      default     = "cax11";
      description = "Hetzner server type (cax11 = ARM64, 2 vCPU, 4 GB RAM)";
    };
    location = {
      type        = "string";
      default     = "nbg1";
      description = "Hetzner datacenter (fsn1=Falkenstein, nbg1=Nuremberg, hel1=Helsinki)";
    };
    image = {
      type    = "string";
      default = "debian-12";
    };
  };
}
