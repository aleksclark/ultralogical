# Multi-binary image: cored + coreworker + coreadmin + core CLI.
# Build stage pinned by digest for reproducibility.
FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Non-root build user not required; final image runs as nonroot.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cored ./cmd/cored \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/coreworker ./cmd/coreworker \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/coreadmin ./cmd/coreadmin \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/core ./cmd/core

# Distroless static nonroot — no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
COPY --from=build /out/cored /out/coreworker /out/coreadmin /out/core /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080 8081 8082
# CMD (not ENTRYPOINT) so Nomad/compose can select coreworker/coreadmin via command.
CMD ["/usr/local/bin/cored"]
