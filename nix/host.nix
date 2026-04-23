{
  config,
  lib,
  pkgs,
  clubConfig,
  ...
}:

let
  cfg = config.services.brygge;

  elementWeb = pkgs.element-web.override {
    conf = {
      default_server_config."m.homeserver" = {
        base_url = "https://matrix.${cfg.domain}";
        server_name = "matrix.${cfg.domain}";
      };
      default_server_name = "matrix.${cfg.domain}";
      brand = "Brygge";
      disable_guests = true;
      disable_3pid_login = true;
    };
  };
in
{
  imports = [
    ./disko.nix
  ];

  system.stateVersion = "25.05";
  nixpkgs.hostPlatform = "x86_64-linux";

  boot = {
    loader.grub = {
      enable = true;
      efiSupport = false;
    };
    kernelParams = [ "console=ttyS0,19200n8" ];
    tmp.cleanOnBoot = true;

    # Hetzner Cloud VMs are KVM/QEMU guests with virtio devices.
    # Without these the initrd cannot find /dev/sda and the kernel
    # hangs silently after the GRUB "booting the kernel" message.
    initrd.availableKernelModules = [
      "ahci"
      "xhci_pci"
      "virtio_pci"
      "virtio_scsi"
      "virtio_blk"
      "virtio_net"
      "sd_mod"
      "sr_mod"
    ];
    initrd.kernelModules = [
      "virtio_balloon"
      "virtio_console"
      "virtio_rng"
    ];
  };

  services.qemuGuest.enable = true;

  networking = {
    hostName = clubConfig.hostname;
    useDHCP = lib.mkDefault true;

    firewall = {
      enable = true;
      allowedTCPPorts = [
        22
        80
        443
      ];
      allowedUDPPorts = [
        443
      ];
    };
  };

  time.timeZone = clubConfig.timezone;

  users.users.root.openssh.authorizedKeys.keys = clubConfig.adminSshKeys;

  services.openssh = {
    enable = true;
    settings = {
      PermitRootLogin = "prohibit-password";
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
    };
  };

  security.sudo.wheelNeedsPassword = false;

  nix.settings = {
    experimental-features = [
      "nix-command"
      "flakes"
    ];
    trusted-users = [
      "root"
      "@wheel"
    ];
    auto-optimise-store = true;
  };

  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 14d";
  };

  environment.systemPackages = with pkgs; [
    git
    htop
    tmux
    vim
    curl
    jq
    postgresql_16
    redis
    go-migrate
  ];

  services.postgresql = {
    enable = true;
    package = pkgs.postgresql_16;

    ensureDatabases = [
      "brygge"
      "dendrite"
    ];

    ensureUsers = [
      {
        name = "brygge";
        ensureDBOwnership = true;
      }
      {
        name = "dendrite";
        ensureDBOwnership = true;
      }
    ];

    authentication = lib.mkForce ''
      local all all              peer map=system-to-pg
      host  all all 127.0.0.1/32 scram-sha-256
      host  all all ::1/128      scram-sha-256
    '';

    identMap = ''
      system-to-pg root     postgres
      system-to-pg postgres postgres
      system-to-pg brygge   brygge
      system-to-pg dendrite dendrite
    '';

    settings = {
      max_connections = 100;
      shared_buffers = "256MB";
      effective_cache_size = "1GB";
    };
  };

  services.redis.servers.brygge = {
    enable = true;
    user = "brygge";
    unixSocket = "/run/redis-brygge/redis.sock";
    unixSocketPerm = 660;
    port = 0;
    settings = {
      maxmemory = "256mb";
      maxmemory-policy = "allkeys-lru";
    };
  };

  services.brygge = {
    enable = true;
    package = pkgs.brygge;
    domain = clubConfig.domain;
    environmentFile = "/etc/brygge/env";
  };

  users.users.brygge.extraGroups = [ config.services.redis.servers.brygge.user ];

  services.dendrite = {
    enable = true;
    httpPort = 8008;
    settings = {
      global = {
        server_name = "matrix.${cfg.domain}";
        private_key = "/var/lib/dendrite/matrix_key.pem";
        trusted_third_party_id_servers = [
          "matrix.org"
          "vector.im"
        ];
        database = {
          connection_string = "postgres:///dendrite?host=/run/postgresql&sslmode=disable";
        };
      };
      client_api = {
        registration_disabled = true;
        registration_shared_secret = "$REGISTRATION_SHARED_SECRET";
      };
      logging = [
        {
          type = "std";
          level = "info";
        }
      ];
    };
  };

  systemd.services.dendrite = {
    after = [ "postgresql.service" ];
    requires = [ "postgresql.service" ];
    preStart = lib.mkAfter ''
      if [ ! -f /var/lib/dendrite/matrix_key.pem ]; then
        ${pkgs.dendrite}/bin/generate-keys -private-key /var/lib/dendrite/matrix_key.pem
      fi
    '';
  };

  services.uptime-kuma = {
    enable = true;
    settings = {
      HOST = "127.0.0.1";
      PORT = "3001";
    };
  };

  services.caddy = {
    enable = true;
    email = clubConfig.adminEmail;

    virtualHosts."${cfg.domain}".extraConfig = ''
      encode gzip zstd
      reverse_proxy 127.0.0.1:${toString cfg.port}
    '';

    virtualHosts."matrix.${cfg.domain}".extraConfig = ''
      reverse_proxy 127.0.0.1:8008
    '';

    virtualHosts."element.${cfg.domain}".extraConfig = ''
      root * ${elementWeb}
      encode gzip zstd
      file_server
      try_files {path} /index.html
    '';

    virtualHosts."status.${cfg.domain}".extraConfig = ''
      reverse_proxy 127.0.0.1:3001
    '';
  };

  services.fail2ban = {
    enable = lib.mkDefault true;
    maxretry = 5;
  };

  system.autoUpgrade = {
    enable = false;
  };

  services.journald.extraConfig = ''
    SystemMaxUse=500M
    MaxRetentionSec=1month
  '';
}
