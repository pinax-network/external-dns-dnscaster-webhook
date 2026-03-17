FROM debian:stable-slim

RUN apt-get update && apt-get -uy upgrade
RUN apt-get -y install ca-certificates && update-ca-certificates

FROM golang:alpine

COPY --from=0 /etc/ssl/certs /etc/ssl/certs
COPY main /

USER 65534
EXPOSE 8888
CMD ["/main"]
