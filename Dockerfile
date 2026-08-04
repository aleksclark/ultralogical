# Multi-binary image: cored + coreworker + coreadmin + core CLI.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/cored ./cmd/cored \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/coreworker ./cmd/coreworker \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/coreadmin ./cmd/coreadmin \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/core ./cmd/core

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cored /out/coreworker /out/coreadmin /out/core /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080 8081 8082
# CMD (not ENTRYPOINT) so Nomad/compose can select coreworker/coreadmin via command.
CMD ["/usr/local/bin/cored"]
