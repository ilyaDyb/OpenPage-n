FROM golang:1.22.12-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY engine-api/ ./engine-api/
RUN CGO_ENABLED=0 go build -trimpath -o /engine-api ./engine-api

FROM scratch
COPY --from=build /engine-api /engine-api
USER 65532:65532
EXPOSE 4444
ENTRYPOINT ["/engine-api"]
