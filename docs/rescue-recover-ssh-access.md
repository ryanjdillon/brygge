# Recover SSH access via Hetzner rescue mode

When `nix run .#deploy` rewrites `/etc/ssh/authorized_keys.d/root` from a
broken `terraform/terraform.tfvars.json` (e.g. tfvars reset to placeholder
contents and a deploy lands before it's fixed), root SSH is lost. NixOS's
declarative authorized keys live at `/etc/ssh/authorized_keys.d/root`, not
`/root/.ssh/authorized_keys` — so you have to mount the installed disk in
rescue mode and patch that file directly.

## Symptoms

- `ssh root@<host>` returns `Permission denied (publickey)` for every key
  your agent + ssh defaults try.
- Verbose `ssh -v` shows the server *accepting the connection* (host key
  exchange completes) but rejecting every offered pubkey.
- `cat /etc/ssh/authorized_keys.d/root` on the installed system shows a
  placeholder/example key instead of the real `admin_ssh_keys` from
  `terraform/terraform.tfvars.json`.

## Recovery procedure

### Recommended: boot the previous NixOS generation

This is the fastest path when the bad deploy is the *most recent*
generation and an earlier generation still has working keys. No mounting
or file editing required.

1. **Enable rescue + reboot to get to a serial/VNC console.** Hetzner
   Cloud panel → server → "Rescue" → enable (any pubkey is fine here, you
   won't need to SSH in) → "Enable rescue & power cycle". This puts you
   into a state where the Hetzner web console is responsive.

2. **Disable rescue and reboot again.** Toggle Rescue off in the Hetzner
   panel and power-cycle. Open the web console (noVNC) immediately so
   you catch the GRUB / systemd-boot menu.

3. **At the boot menu, pick a previous NixOS generation.** GRUB lists
   them as `NixOS - Configuration N (...)`; systemd-boot lists them under
   the "older configurations" submenu. Pick the most recent one *before*
   the broken deploy — it'll still have your real `admin_ssh_keys`
   baked into `/etc/ssh/authorized_keys.d/root`.

4. **SSH back in normally** once the system is up:

   ```
   ssh root@<host-ip>
   ```

5. **Re-deploy from a known-good tfvars** so the *current* generation
   becomes correct again (see "Permanent fix" below). After the deploy,
   the broken generation can be garbage-collected on the next
   `nix-collect-garbage --delete-older-than ...` run.

### Fallback: edit `authorized_keys.d/root` from rescue

Use this when no previous generation has a working key (e.g. multiple
bad deploys in a row, or GC already removed the good ones).

1. **Boot into Hetzner rescue.** Enable rescue with one of your current
   pubkeys, power-cycle, and SSH in:

   ```
   ssh -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519 root@<host-ip>
   ```

2. **(Optional) Switch the rescue console to US keymap.** Useful if you
   land in the Hetzner web console with a German layout. The default
   rescue image doesn't ship `loadkeys` keymaps, so install them first:

   ```
   apt-get update
   apt-get install -y console-data kbd
   loadkeys us
   ```

   (Locale ≠ keymap; setting `LANG` doesn't change which physical key
   produces `/`. `localectl set-keymap us` also works.)

3. **Mount the installed NixOS root.** Identify the partition with
   `lsblk`. On the standard Hetzner Cloud + NixOS install the root is
   `/dev/sda2`:

   ```
   mkdir -p /mnt
   mount /dev/sda2 /mnt
   ```

   (LUKS/ZFS variants need extra steps; this guide assumes the unencrypted
   layout we deploy today.)

4. **Append your current pubkey to the declarative root authorized keys
   file.** This is the file NixOS regenerates on every activation from
   `users.users.root.openssh.authorizedKeys.keys = clubConfig.adminSshKeys`
   (i.e. `terraform/terraform.tfvars.json → admin_ssh_keys`). It is a
   plain text file, *not* a Nix-store symlink, so you can edit it
   directly:

   ```
   cat >> /mnt/etc/ssh/authorized_keys.d/root <<'EOF'
   ssh-ed25519 AAAA…your full pubkey here… you@host
   EOF
   chmod 600 /mnt/etc/ssh/authorized_keys.d/root
   sync
   ```

   A blank line between entries is fine — sshd ignores blank lines.

5. **Disable rescue, then reboot.** This is the step that previously
   tripped us up: if you `reboot` while the rescue toggle is still
   *enabled* in the Hetzner panel, the next boot lands back in rescue
   instead of the installed system, so your edit to `/mnt/etc/...` has
   no effect. Toggle Rescue *off* in the Hetzner panel **first**, then:

   ```
   reboot
   ```

   Then SSH:

   ```
   ssh root@<host-ip>
   ```

   should now succeed using the key you appended.

   To confirm you're on the installed system and not rescue, your
   `~/.ssh/known_hosts` entry for the host should match the same
   fingerprint as before:

   ```
   ssh-keygen -lF <host-ip>
   ```

## Permanent fix — re-sync tfvars

The next deploy rewrites `/etc/ssh/authorized_keys.d/root` from tfvars,
so the manual append in step 4 is temporary. Before re-deploying:

```
jq '.admin_ssh_keys' terraform/terraform.tfvars.json
```

confirm the array contains the key you appended (or any current key of
yours). Then commit and deploy:

```
git diff terraform/terraform.tfvars.json
git commit -m "chore(deploy): restore real admin_ssh_keys"
git push
nix run .#deploy -- <host-ip>
```

## How this happens

`terraform/terraform.tfvars.json` is **not** in `.gitignore` (so it's
visible to the flake build) but it holds per-club secrets and per-deployer
identity. If anyone resets it to placeholder/example contents (deliberate
sanitisation, accidental `git checkout`, or a stash collapse) and a
deploy lands before they restore it, the new NixOS generation bakes the
placeholder keys into `/etc/ssh/authorized_keys.d/root`, locking out
everyone whose key isn't in the placeholder set.

To prevent recurrence, either:

- always run deploys from a state where `admin_ssh_keys` is verified
  (`jq '.admin_ssh_keys' terraform/terraform.tfvars.json | ssh-keygen -lf
  /dev/stdin`), or
- track the file via something like `git update-index --skip-worktree`
  *with care* (`flake.nix` warns this hides changes from Nix and can
  break deploys), or
- adopt sops-nix / agenix so authorized_keys aren't checked in at all.
