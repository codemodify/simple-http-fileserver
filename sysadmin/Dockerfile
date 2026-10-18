FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /simple-http-fileserver .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && \
    mkdir -p /var/www
COPY --from=build /simple-http-fileserver /usr/local/bin/simple-http-fileserver

USER nobody

VOLUME ["/var/www"]

EXPOSE 64080
ENTRYPOINT ["simple-http-fileserver", "--folder", "/var/www", "--enable-http", "--http-on", ":64080"]