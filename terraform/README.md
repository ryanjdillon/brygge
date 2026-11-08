# terraform/

Terranix modules for one Terraform root with one state. Deploy steps and
the tfvars reference are in [docs/developer/deploy.md](../docs/developer/deploy.md).

```
config.nix       imports every module below
providers.nix    hcloud + aws providers (explicit keys; see aws/ses.nix)
backend.nix      S3 state on Hetzner Object Storage
variables.nix    every tfvars key
outputs.nix
hetzner/         Hetzner Cloud: server.nix (VM, firewall, rDNS), dns.nix (zone records)
aws/             Amazon SES: ses.nix (identity, configuration set, SNS → SQS, IAM);
                 account, IAM policy and keys in docs/developer/aws-setup.md
```

Provider directories group modules; they are not separate roots. Values
cross between them by plain interpolation in the one root (the SES DKIM
tokens from `aws/ses.nix` become CNAMEs in `hetzner/dns.nix`), and
Terraform addresses resources by name, not by file, so moving a module
between directories changes nothing in state.

`terraform.tfvars.json` is a tracked placeholder that deployers fill in
locally; never commit real values (the pre-commit hook refuses them).
