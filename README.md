# mc-proxy

[![Docker](https://github.com/Andacious/mc-proxy/actions/workflows/docker.yml/badge.svg)](https://github.com/Andacious/mc-proxy/actions/workflows/docker.yml)

`mc-proxy` lets Minecraft Bedrock consoles use custom servers through the
featured-server list. It provides:

- a forwarding DNS server that overrides configured featured-server hostnames;
- one UDP proxy per override, forwarding Bedrock traffic to any hostname and
  port;
- a default configuration, stored in a Docker volume, that maps the current
  featured servers to themselves;
- a simple web UI for editing that configuration; and
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

On first start the container writes a default `config.yaml` into the
`mc-proxy-config` Docker volume mounted at `/etc/mc-proxy`. The default maps
each of the current Bedrock featured servers to itself, so the proxy passes
traffic straight through until the targets are changed:

```text
geo.hivebedrock.network -> 192.168.1.241:19132 -> container:19132 -> geo.hivebedrock.network:19132
mco.cubecraft.net       -> 192.168.1.242:19132 -> container:19133 -> mco.cubecraft.net:19132
mco.lbsg.net            -> 192.168.1.243:19132 -> container:19134 -> mco.lbsg.net:19132
play.galaxite.net       -> 192.168.1.244:19132 -> container:19135 -> play.galaxite.net:19132
play.inpvp.net          -> 192.168.1.245:19132 -> container:19136 -> play.inpvp.net:19132
```

Edit the values in the web UI at `http://<docker-host>:8080/`, or edit the file
in the volume directly. `config.example.yaml` in this repository shows the same
defaults. The UI writes the file only when the whole configuration is valid;
restart the container to apply saved changes:

```sh
docker compose restart mc-proxy
```

The UI has no authentication, so publish port `8080` only on a trusted
network. Pass `-ui-listen ""` to disable it, or bind it to a specific address
with `-ui-listen 127.0.0.1:8080`.

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

`compose.yaml` publishes one port per default mapping. Update it whenever
mappings are added, removed, or renumbered:

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
  - domain: "geo.hivebedrock.network"
    proxy_ip: "192.168.1.241"
    listen: ":19132"
    target: "geo.hivebedrock.network:19132"
    idle_timeout: "2m0s"
```

Both UDP and TCP DNS are supported. Bedrock game traffic is UDP.

## Capacity testing

The integration suite verifies 25 simultaneous clients forwarding 1,000 total
UDP packets without loss, while enforcing a one-second maximum p95 loopback
latency. Run it with race detection:

```sh
go test -race -run TestUDPProxyHandles25ConcurrentClients ./internal/proxy
```

Benchmarks for 20 and 100 concurrent clients report packet throughput, latency
per operation, memory allocations, and bytes processed:

```sh
go test -run '^$' -bench BenchmarkUDPProxyConcurrentClients -benchmem ./internal/proxy
```

Loopback benchmarks validate proxy overhead rather than real-world Internet
performance. Run them on the intended Docker host and test separately against
the actual backend and network path before choosing a production capacity.
