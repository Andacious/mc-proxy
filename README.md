# mc-proxy

[![Docker](https://github.com/Andacious/mc-proxy/actions/workflows/docker.yml/badge.svg)](https://github.com/Andacious/mc-proxy/actions/workflows/docker.yml)

`mc-proxy` lets Minecraft Bedrock consoles use custom servers through the
featured-server list. It provides:

- a forwarding DNS server that overrides configured featured-server hostnames;
- one UDP proxy per override, forwarding Bedrock traffic to any hostname and
  port; and
- a small, non-root Docker image.

This project is intended for home self-hosting. Multiple targets may use the
same dynamic-DNS hostname or public IP with different forwarded ports.

## Why each mapping needs a LAN IP

Bedrock consoles connect to featured servers on UDP port `19132`, and the
initial UDP traffic does not include the hostname selected by the player. A
single proxy IP therefore cannot reliably distinguish multiple featured-server
entries.

Each mapping uses a dedicated private IPv4 address. DNS returns that address,
and Docker translates `<mapping IP>:19132` to a unique port inside the
container:

```text
featured-one.example.net -> 192.168.1.241:19132 -> container:19132 -> target:20001
featured-two.example.net -> 192.168.1.242:19132 -> container:19133 -> target:20002
```

The dedicated addresses must belong to the Docker host and must not be in your
router's DHCP allocation range.

## Setup

### 1. Reserve LAN addresses

Choose one unused private address per mapping. Add each address to the Docker
host's LAN interface. For a temporary Linux setup:

```sh
sudo ip address add 192.168.1.241/24 dev eth0
sudo ip address add 192.168.1.242/24 dev eth0
```

Use your operating system's network configuration to make these aliases
persistent. Replace `eth0`, the subnet, and the addresses with values for your
LAN.

### 2. Configure mappings

Copy the example configuration:

```sh
cp config.example.yaml config.yaml
```

For every mapping:

- `domain` is the featured-server hostname to intercept;
- `proxy_ip` is its dedicated LAN address;
- `listen` is a unique internal UDP port;
- `target` is the custom Bedrock server hostname/IP and UDP port; and
- `idle_timeout` controls how long an inactive client session is retained.

The target hostname is resolved whenever a new client session starts, so a
dynamic-DNS hostname is supported.

Unmatched DNS requests are sent to `dns.forwarders`. This allows consoles to
use `mc-proxy` as their normal DNS server rather than only for the configured
overrides.

### 3. Match Docker ports to the configuration

Update `compose.yaml` for every mapping:

```yaml
ports:
  - "192.168.1.241:19132:19132/udp"
  - "192.168.1.242:19132:19133/udp"
```

The first address is `proxy_ip`, the external port remains `19132`, and the
last port must match that mapping's `listen` port.

### 4. Start the service

Build and start the service locally:

```sh
docker compose up -d --build
```

Images for `linux/amd64` and `linux/arm64` are also published to GitHub
Container Registry from `master` and version tags:

```sh
docker pull ghcr.io/andacious/mc-proxy:latest
```

Configure the console's primary DNS server to the Docker host's regular LAN
address. Leave a secondary DNS server unset; otherwise the console may bypass
the configured overrides.

The Docker host must have TCP and UDP port `53` available. Stop or reconfigure
any local DNS listener already using that port before starting the container.

View startup and forwarding errors with:

```sh
docker compose logs -f mc-proxy
```

## Configuration reference

```yaml
dns:
  listen: ":5353"
  forwarders: ["1.1.1.1:53", "8.8.8.8:53"]
  ttl: 60

mappings:
  - domain: "featured-one.example.net"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "my-home-server.example.org:20001"
    idle_timeout: "2m"
```

Both UDP and TCP DNS are supported. Bedrock game traffic is UDP.
