FROM golang:1.22-alpine AS builder

RUN apk --update --no-cache add \
    binutils \
    && rm -rf /root/.cache
WORKDIR /go/src/github.com/lsst-dm/s3nd
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-extldflags '-static'" -o s3nd && strip s3nd

FROM alpine:3
WORKDIR /root/
COPY --from=builder /go/src/github.com/lsst-dm/s3nd/s3nd /bin/s3nd
ENTRYPOINT ["/bin/s3nd"]
