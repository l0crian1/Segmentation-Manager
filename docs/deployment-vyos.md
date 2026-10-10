# Deploy on VyOS 1.5

[Documentation home](../README.md)

Segmentation Manager can run on a VyOS 1.5 router using its native container service. The application still manages routers through their HTTPS APIs, including the hosting router if you add it as a device.

## Prerequisites

- VyOS 1.5 with native container support, sufficient free storage for the image and application data, and capacity for an additional management service.
- Working DNS and outbound HTTPS access for downloading `docker.io/l0crian/segman`.
- A free TCP port 8443 and access to that port from your management network.
- Reachability from the hosting router to each managed router's HTTPS API.

Choose either host networking or a dedicated container network below. The application needs no additional container capabilities or host devices. Start with the 512 MiB memory limit shown below and monitor usage alongside the router's workload.

The examples use the [VyOS 1.5 container interface](https://docs.vyos.io/en/1.5/configuration/container/). Configuration blocks contain only `set` commands; apply and persist them through your normal VyOS configuration workflow. Run installation, update, and verification commands in operational mode.

## Pull the published image

In operational mode:

```text
add container image docker.io/l0crian/segman:latest
sudo install -d -o 10001 -g 10001 -m 0700 /config/containers/segman
```

For controlled updates, substitute an available version tag for `latest` in both the pull command and the configuration. The persistent directory is separate from the image and must survive container replacement. Include it in your backup process; saving the router configuration does not back up application data.

## Configure and start

Use this core configuration together with **one** of the networking alternatives immediately below:

```text
set container name segman image 'docker.io/l0crian/segman:latest'
set container name segman description 'Segmentation Manager'
set container name segman uid '10001'
set container name segman gid '10001'
set container name segman memory '512'
set container name segman restart 'always'
set container name segman volume data source '/config/containers/segman'
set container name segman volume data destination '/data'
set container name segman volume data mode 'rw'
set container name segman tmpfs temporary destination '/tmp'
set container name segman tmpfs temporary size '128'
set container name segman health-check
```

### Option A: Host networking

The application listens directly on the router's addresses at TCP 8443. Restrict access using the router's local-input firewall policy. Do not combine this option with a named container network or port mapping.

```text
set container name segman allow-host-networks
```

### Option B: Container networking

Attach the container to a dedicated network and publish TCP 8443 on the router's management address using the container configuration. Netavark handles the port forwarding. Use this option without `allow-host-networks`.

Replace `192.0.2.1` with the router's management address and choose a container subnet that does not overlap your existing networks. This example assigns `172.31.255.2` to the container; the first address in the subnet is reserved for its gateway.

```text
set container name segman network segman-net address '172.31.255.2'
set container name segman port 8443 destination '8443'
set container name segman port 8443 listen-address '192.0.2.1'
set container name segman port 8443 protocol 'tcp'
set container name segman port 8443 source '8443'
set container network segman-net prefix '172.31.255.0/29'
```

Restrict web access to your management network and allow the container to reach managed routers' HTTPS APIs. API allow-lists must permit the source address each router actually sees.

### Verify and open the application

After applying the core configuration and your chosen network option, verify in operational mode:

```text
show container
show container log segman
```

For the dedicated network, also check:

```text
show container network
```

Open **https://ROUTER_MANAGEMENT_IP:8443** and complete [initial setup](administration.md#initial-setup) with the one-time claim code from the log. The application initially uses a self-signed HTTPS certificate. The application's port 8443 and the router's own HTTPS API port, usually 443, are separate services.

Application settings belong in the web interface. If a startup override is necessary, VyOS supports `set container name segman environment DATA_DIR value '/data'` and the same pattern for `LISTEN_ADDR`. Their defaults already match this deployment. Changing the listen address or port also requires adapting the container healthcheck and management firewall rules; see [environment variables](deployment-docker.md#application-environment-variables).

For router API certificate enrollment, follow [Obtaining a VyOS certificate fingerprint](administration.md#obtaining-a-vyos-certificate-fingerprint).

## Updates and restarts

Back up the application and wait for pending router operations to complete before restarting it.

Update the image referenced by the configured container, then restart it to use that image:

```text
update container image segman
restart container segman
```

Verify separately:

```text
show container
show container log segman
```

For a pinned installation, updating the image keeps the configured tag; it does not select a newer version. To move to another release, replace `VERSION` with its published tag and pull it first in operational mode:

```text
add container image docker.io/l0crian/segman:VERSION
```

Then change the configured image through your normal configuration workflow:

```text
set container name segman image 'docker.io/l0crian/segman:VERSION'
```

Verify after applying the change:

```text
show container
show container log segman
```

The native service recreates the container using its configured image. Keep `/config/containers/segman` and the volume settings unchanged. Verify application health, sign in, and refresh a managed router after the update. For rollback, restore a pre-upgrade backup with the previous application image.
