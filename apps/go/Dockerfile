# syntax=docker/dockerfile:1
# Builds one binary from cmd/ (CMD build arg) into a distroless, non-root image.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD=quote-prober
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
COPY scenarios.yaml /etc/quote-prober/scenarios.yaml
ENV SCENARIOS_FILE=/etc/quote-prober/scenarios.yaml
USER nonroot:nonroot
ENTRYPOINT ["/app"]
