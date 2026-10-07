# Qadrant server: one static binary on a minimal image with CA certificates.
FROM docker.io/library/golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# timetzdata embeds the time zone database, so TZ works without system files.
RUN CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags "-s -w" -o /qadrant-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /qadrant-server /qadrant-server
VOLUME /data
EXPOSE 8090
ENV TZ=Europe/Madrid
ENTRYPOINT ["/qadrant-server", "serve", "--http", "0.0.0.0:8090", "--dir", "/data"]
