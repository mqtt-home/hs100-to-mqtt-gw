## ADDED Requirements

### Requirement: Makefile targets

The repository SHALL provide a `Makefile` at `app/Makefile` with at
least the targets `build`, `test`, `lint`, `run`, and `docker`,
matching the shape used by `hue-to-mqtt-gw` and `velux-mqtt-gw`.

#### Scenario: Local build

- **WHEN** the developer runs `make build`
- **THEN** a `hs2mqtt` binary is produced under `app/`

#### Scenario: Test

- **WHEN** the developer runs `make test`
- **THEN** all Go tests run and pass

### Requirement: Multi-arch Docker images

The repository SHALL provide `Dockerfile.goreleaser` and
`Dockerfile.goreleaser-arm` that consume a pre-built binary and copy
it into a distroless base image, matching the sibling bridges'
approach.

#### Scenario: Image build

- **WHEN** goreleaser produces the images
- **THEN** an `amd64` image and an `arm/v7` image are published, each based on distroless

### Requirement: GoReleaser configuration

The repository SHALL provide `.goreleaser.yml` at `app/.goreleaser.yml`
that builds `linux/amd64` and `linux/arm/v7` binaries, sets ldflags
that populate `version.Version`, `version.GitCommit`, and
`version.BuildTime`, and produces the Docker images defined by the
Dockerfiles above.

#### Scenario: Version stamping

- **WHEN** a tagged release is built
- **THEN** the binary reports the tag as `version.Version` and the commit hash as `version.GitCommit`

### Requirement: Container registry

The repository's release pipeline SHALL publish the Docker images to
`ghcr.io/mqtt-home/hs100-to-mqtt-gw` under three tags per release:
the exact tag (`{{ .Tag }}`), the major.minor (`{{ .Major }}.{{ .Minor }}`),
and `latest`.

#### Scenario: Tagged release

- **WHEN** a `v1.2.3` release is cut
- **THEN** three tags are pushed to `ghcr.io/mqtt-home/hs100-to-mqtt-gw`: `1.2.3`, `1.2`, and `latest`

#### Scenario: Multi-arch tags

- **WHEN** the images are pulled by `docker pull ghcr.io/mqtt-home/hs100-to-mqtt-gw:latest`
- **THEN** the manifest resolves to `linux/amd64` on x86_64 hosts and to `linux/arm/v7` on armhf hosts

### Requirement: Config example

The repository SHALL ship `production/config/config-example.json`
containing a runnable sample with two devices, matching the
`hue-to-mqtt-gw` convention.

#### Scenario: Copy and run

- **WHEN** the operator copies `config-example.json` to `config.json` and edits `mqtt.url` and the device `host`/`name` values
- **THEN** the resulting file is a valid input to the binary
