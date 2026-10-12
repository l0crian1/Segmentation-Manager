# Segmentation Manager

Segmentation Manager is a web application for managing VRFs and BGP route-target import policies on VyOS routers. Use separate MPLS VPN and VXLAN/EVPN connectivity matrices to review and apply segmentation changes from one place.

[Source code](https://github.com/l0crian1/Segmentation-Manager) · [Releases](https://github.com/l0crian1/Segmentation-Manager/releases) · [Docker Hub](https://hub.docker.com/r/l0crian/segman)

<img width="1263" height="992" alt="image" src="https://github.com/user-attachments/assets/1c22a4b1-d776-4119-96dc-67ff2bf34551" />
<img width="1266" height="1139" alt="image" src="https://github.com/user-attachments/assets/1deaee4d-b4cf-4ee4-86f3-2ca81d3f90a8" />


## Features

- Organize routers into sites and manage their API connections and certificate trust.
- Create, edit, and delete VRFs, with independent MPLS and VXLAN capabilities.
- Manage MPLS VPN imports for IPv4 and IPv6, including same-VRF imports between PEs.
- Manage EVPN imports using explicitly configured export route-targets, including deployments with a shared external VXLAN interface.
- Preview configuration commands, apply changes, and verify the router's resulting configuration.
- Control access with administrator, network-admin, and view-only accounts.
- Use light, dark, or system appearance, encrypted backups, and configuration activity records.

Segmentation Manager configures VRFs and their route-target policies on existing network infrastructure. MPLS transport, BGP peerings, VXLAN interfaces, bridging, and route redistribution remain operator-managed.

## Requirements

- A Docker host running Linux containers, or a VyOS 1.5 router with native container support.
- Persistent storage mounted at `/data` and management-network access to the application and router HTTPS APIs.
- A VyOS router with the HTTPS API enabled. All release versions can use VRF management and the connectivity matrices; there is no version allow-list. See [compatibility and limits](docs/compatibility.md) for tested releases and configuration requirements.

## Get started

Pull the published image from **`l0crian/segman`**. No separate application, database, or build-tool installation is required.

```sh
docker pull l0crian/segman:latest
docker run -d --name segman --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,noexec,nosuid,size=128m \
  -p 127.0.0.1:8443:8443 -v segman-data:/data \
  l0crian/segman:latest
docker logs segman
```

Open **https://localhost:8443** on the Docker host. The initial certificate is self-signed. Use the one-time claim code from the startup log to create your first administrator, then add a site and router. For access from another computer, publish the port on the host's management address as described in the [Docker deployment guide](docs/deployment-docker.md).

For controlled upgrades, pin a published version tag instead of `latest`.

## Documentation

- [Docker deployment](docs/deployment-docker.md): published images, Compose, local container builds, storage, and updates.
- [VyOS 1.5 deployment](docs/deployment-vyos.md): run the application using VyOS native containers.
- [User guide](docs/user-guide.md): device setup, VRFs, MPLS/EVPN matrices, and applying configuration.
- [Administration](docs/administration.md): first setup, roles, settings, TLS, backups, and recovery.
- [Compatibility and troubleshooting](docs/compatibility.md): supported versions, policy limits, and resolving problems.
- [Publishing releases](docs/publishing.md): maintainer setup for GitHub Actions and Docker Hub.
