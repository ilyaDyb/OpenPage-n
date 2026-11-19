FROM golang:1.22.12-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY source-watcher/ ./source-watcher/
RUN CGO_ENABLED=0 go build -trimpath -o /source-watcher ./source-watcher

FROM alpine:3.22
RUN apk add --no-cache git ca-certificates
COPY --from=build /source-watcher /source-watcher
USER 65532:65532
ENTRYPOINT ["/source-watcher"]
