# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS builder

ARG REPO_SSH_URL
ARG REPO_BRANCH=main

RUN apk add --no-cache git openssh

WORKDIR /src

RUN --mount=type=ssh \
    mkdir -p /root/.ssh && \
    ssh-keyscan bitbucket.org >> /root/.ssh/known_hosts && \
    git clone --depth 1 --branch "${REPO_BRANCH}" "${REPO_SSH_URL}" .

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/lexmodo-server ./cmd/server

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /out/lexmodo-server /app/lexmodo-server
COPY --from=builder /src/config.json /app/config.json

RUN mkdir -p /app/files

EXPOSE 50050 50051

ENTRYPOINT ["/app/lexmodo-server"]
 
