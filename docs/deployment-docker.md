# Deploy on Docker

[Documentation home](../README.md)

The recommended installation uses the published **`l0crian/segman`** image. It includes the web interface, HTTPS service, and embedded database. Choose either Docker CLI or Compose; they are alternatives.

## Prerequisites

- Docker Engine or Docker Desktop in Linux-container mode. Install the Compose plugin if using Compose.
- A Linux AMD64 or ARM64 host, persistent local storage, and outbound access to Docker Hub.
- Network access from the container to each router's HTTPS API, normally TCP 443.
- An available management port, normally TCP 8443, and a current browser.

The commands below use a POSIX shell, such as a Linux terminal or WSL. Publish the application only on an appropriate management address and restrict access to your administrators. Container port publishing can interact with host firewall rules; apply restrictions at the Docker/network boundary as appropriate for your host.

## Option 1: Published image with Docker CLI

```sh
docker pull l0crian/segman:latest
docker run -d --name segman --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,noexec,nosuid,size=128m \
  -p 127.0.0.1:8443:8443 -v segman-data:/data \
  l0crian/segman:latest
docker logs segman
```

This creates a persistent named volume and exposes HTTPS to the Docker host itself at **https://localhost:8443**. For remote access, replace `127.0.0.1` with the Docker host's management IP. For example, `-p 192.0.2.10:8443:8443` serves **https://192.0.2.10:8443** when that address belongs to your host. Use your real address in place of this documentation example.

To use a different host port, change only the first port number, for example `-p 127.0.0.1:9443:8443`. Keep the container's port at 8443.

Follow [initial setup](administration.md#initial-setup) using the claim code from the log. There are no default login credentials.

## Option 2: Published image with Compose

Save the repository's [compose.yaml](../compose.yaml) in a dedicated directory. A source checkout is unnecessary. From that directory:

```sh
docker compose pull
docker compose up -d
docker compose logs segmentation-manager
```

Compose also defaults to **https://localhost:8443**. To select a published version, remote management address, or different host port, put these optional Compose settings in a `.env` file beside `compose.yaml`:

```dotenv
SEGMAN_TAG=latest
SEGMAN_BIND_ADDRESS=192.0.2.10
SEGMAN_PORT=8443
```

Replace the example IP with an address on your Docker host. Replace `latest` with an available version such as `1.0.0` when pinning a release. These variables configure Compose's image selection and port publishing; they are not application environment variables.

Keep the same Compose directory and project name during updates so Compose reuses its existing volume. The Compose volume and the `segman-data` volume in the Docker CLI example are separate installations. When switching deployment methods, explicitly reuse the existing volume or restore a backup instead of starting against empty storage.

## Persistent storage

Mount a writable volume at `/data`. It contains the application database, encrypted router credentials, their encryption key, and application TLS material. Preserve the whole directory, use storage with reliable filesystem locking, and run only one application instance against it.

The container runs as UID/GID **10001:10001**. Docker initializes a new named volume with the required ownership. To use a bind mount on a Linux host, prepare its directory first:

```sh
sudo install -d -o 10001 -g 10001 -m 0700 /srv/segman
```

Then replace `-v segman-data:/data` with `-v /srv/segman:/data` in the run command. On SELinux hosts, apply an appropriate container storage label, for example `-v /srv/segman:/data:Z` for a private bind mount. Protect host access to this directory: it includes the key needed to decrypt stored credentials.

See [backup and restore](administration.md#backup-and-restore) before moving or upgrading an installation. Never remove the data volume with `docker compose down -v` or volume-pruning commands.

## Application environment variables

Normal application configuration is in **Settings**. Only these optional environment variables are read at startup:

- **`DATA_DIR`**, default `/data`: writable directory containing application state. If overridden, mount persistent storage at the new path with the same ownership.
- **`LISTEN_ADDR`**, default `:8443`: HTTPS listening address inside the container. Keeping the default allows the packaged healthcheck to work. If changing it, also adjust the healthcheck and container port mapping; the healthcheck normally connects to `127.0.0.1:8443` inside the container.

Docker CLI passes overrides with `-e`; Compose uses the service's `environment` section. No environment file is required, and account passwords and router API keys are entered through the application.

## Optional: Build the container from source

Use a local build when you need to run a particular source revision. Docker performs the entire frontend and backend build; no Go, Node.js, or package-manager installation is required on the host.

Clone the [GitHub repository](https://github.com/l0crian1/Segmentation-Manager) and replace `RELEASE_TAG` with the release you want to build. Omit the checkout command to build the current source before the first release is published.

```sh
git clone https://github.com/l0crian1/Segmentation-Manager.git segmentation-manager
cd segmentation-manager
git checkout RELEASE_TAG
docker compose -f compose.yaml -f compose.build.yaml up -d --build
docker compose logs segmentation-manager
```

The [build override](../compose.build.yaml) uses `segman:local`, preserves the same Compose storage and port settings, and disables registry pulls for that local image. Use both `-f` options for subsequent local-build operations.

Alternatively, build with `docker build -t segman:local .`, then use the Docker CLI run command above with `segman:local` as the image.

## Updates

Take an application backup and record the currently deployed image before upgrading. Wait for router configuration operations to finish. Choose an available release tag from Docker Hub; `latest` follows stable releases, while exact version tags provide a controlled upgrade point.

For Compose, update `SEGMAN_TAG` if pinned, then:

```sh
docker compose pull
docker compose up -d
docker compose ps
```

For Docker CLI, pull the desired image, stop and remove **only the application container**, then repeat your original `docker run` command with the new image and **the same data volume and port settings**:

```sh
docker pull l0crian/segman:latest
docker stop segman
docker rm segman
```

For a local build, check out the desired source release and repeat the two-file Compose command with `--build`.

Accounts and application state are retained in the volume, and required database migrations run at startup. Verify that the container is healthy, sign in, and refresh a router. If reverting an upgrade, use the previous image with a restored pre-upgrade backup; database downgrades are not supported.

## Health and logs

```sh
docker ps --filter name=segman
docker inspect --format '{{.State.Health.Status}}' segman
docker logs --tail 100 segman
```

The image healthcheck checks the application's HTTPS `/healthz` endpoint. A healthy application does not imply that its managed routers are reachable. For Compose deployments, use `docker compose ps` and `docker compose logs --tail 100 segmentation-manager`.
