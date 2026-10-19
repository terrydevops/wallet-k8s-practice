# syntax=docker/dockerfile:1
# Builds one binary from cmd/ (CMD build arg) into a distroless, non-root image.
# The build stage runs on the builder's own platform and cross-compiles, so a multi-arch release
# (amd64 + arm64) needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD=quote-prober
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
COPY scenarios.yaml /etc/quote-prober/scenarios.yaml
ENV SCENARIOS_FILE=/etc/quote-prober/scenarios.yaml
USER nonroot:nonroot
ENTRYPOINT ["/app"]
