# ---- builder ----
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /codedebate .

# ---- runtime ----
FROM alpine:3.21
ARG GLAB_VERSION=1.52.0
RUN apk add --no-cache git ca-certificates \
    && wget -qO- "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_amd64.tar.gz" \
       | tar xz -C /usr/local/bin --strip-components=1 bin/glab

COPY --from=builder /codedebate /usr/local/bin/codedebate

EXPOSE 8080
ENTRYPOINT ["codedebate", "serve"]
