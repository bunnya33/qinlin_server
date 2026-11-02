FROM golang:1.26.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out/data && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/qinlin ./cmd/qinlin

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build --chown=65532:65532 /out/ /app/
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/qinlin"]
