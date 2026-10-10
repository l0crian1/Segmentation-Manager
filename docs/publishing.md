# Publishing releases

[Documentation home](../README.md)

This page is for release maintainers. Users install the published image using the [Docker](deployment-docker.md) or [VyOS](deployment-vyos.md) deployment guide.

## Docker Hub and GitHub setup

1. Create the public Docker Hub image repository **`l0crian/segman`**.
2. In the GitHub repository's **Settings → Secrets and variables → Actions**, create these repository secrets:
   - **`DOCKERHUB_USERNAME`**: a Docker Hub account authorized to publish to `l0crian/segman`.
   - **`DOCKERHUB_TOKEN`**: that account's Docker Hub access token with read/write access to the image repository.
3. Enable GitHub Actions and ensure the [validation](../.github/workflows/ci.yml) and [publishing](../.github/workflows/publish.yml) workflows are present in the release commit. Keep credentials out of files, image build arguments, and release notes.

No repository-specific GitHub URL is hardcoded into the workflow. It uses the repository in which it runs, checks out the release tag, and obtains source metadata from that context.

## Release tags

Publish a GitHub **Release** with a tag of the form `vMAJOR.MINOR.PATCH`, for example `v1.2.3`. A stable release publishes:

- `l0crian/segman:1.2.3`
- `l0crian/segman:1.2`
- `l0crian/segman:1`
- `l0crian/segman:latest`

For a pre-release, use a suffix such as `v1.3.0-rc.1` and mark the GitHub release as a **pre-release**. It publishes only `l0crian/segman:1.3.0-rc.1`; stable aliases and `latest` remain unchanged. For stable `0.x` releases, the workflow omits the broad `0` alias but publishes the exact version, minor version, and `latest`.

The version suffix and GitHub pre-release setting must agree. Tags with leading-zero version numbers, unsupported syntax, or build metadata (`+...`) are rejected. Publishing is triggered by a published release, including a published pre-release; merely pushing a Git tag or saving a release draft does not publish an image.

Publish stable releases in increasing version order: every stable publication updates its major/minor aliases and `latest`. The workflow does not distinguish backport releases or consult GitHub's “latest release” designation. Keep exact version tags associated with their original release; publish a new patch version for changed code.

## What the workflow does

1. Runs the shared validation workflow: builds the frontend, runs Go race tests and static checks, and builds the production container.
2. Validates the release version and computes image tags and OCI metadata.
3. Authenticates to Docker Hub using the repository secrets.
4. Builds and publishes Linux AMD64 and ARM64 images, with provenance and an SBOM, under a multi-platform manifest.
5. Records the published digest in the workflow summary.

The production image includes a non-root service, embedded web assets, a writable `/data` mount point, and an HTTPS healthcheck. The build runs frontend and Go compilation in container stages; the runtime image contains no application build toolchain. The publishing job requires successful validation and runs only on release events, so pull-request checks do not receive Docker Hub credentials.

This uses Docker's official [GitHub Actions integration](https://docs.docker.com/build/ci/github-actions/manage-tags-labels/) and [multi-platform build support](https://docs.docker.com/build/ci/github-actions/multi-platform/).

## Verify a publication

After the workflow succeeds, inspect the published manifest and pull the exact release on a clean Docker host:

```sh
docker buildx imagetools inspect l0crian/segman:1.2.3
docker pull l0crian/segman:1.2.3
```

Replace `1.2.3` with the version just published. Confirm both architecture entries are present, start it using the normal deployment instructions with a fresh test volume, and complete initial setup. Release notes should describe user-visible changes, compatibility requirements, and any upgrade steps. Update the Docker Hub repository overview from the current README when preparing the release.

If publishing fails, inspect the validation/build output and verify that the token can write to the correct Docker Hub repository. Re-running the failed workflow is supported; do not move the release tag to different source code to fix an existing version.
