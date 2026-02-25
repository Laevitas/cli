FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build -ldflags "-s -w \
    -X github.com/laevitas/cli/internal/version.Version=${VERSION} \
    -X github.com/laevitas/cli/internal/version.CommitSHA=${COMMIT} \
    -X github.com/laevitas/cli/internal/version.BuildDate=${BUILD_DATE}" \
    -o /laevitas .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /laevitas /usr/local/bin/laevitas
ENTRYPOINT ["laevitas"]
