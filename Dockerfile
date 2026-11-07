# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/tf-plan-cost ./cmd/tf-plan-cost

# static-debian12 includes CA certificates for the AWS API and runs as nonroot.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tf-plan-cost /usr/local/bin/tf-plan-cost
ENTRYPOINT ["/usr/local/bin/tf-plan-cost"]
