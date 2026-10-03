FROM golang:alpine as builder
WORKDIR /app
ENV IMAGE_TAG=dev
RUN apk update && apk upgrade && apk add --no-cache ca-certificates git
RUN update-ca-certificates
ADD . /app/
ARG VERSION
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w -X 'github.com/coderabbit-demo/gev/cmd.Version=${VERSION}'" -o gev .

FROM scratch

COPY --from=builder /app/gev .
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT ["./gev"]
