FROM golang:1.13
ENV workdir /build
WORKDIR $workdir
COPY . .

RUN go install -v ./cmd/short

VOLUME ["/data"]
WORKDIR /data
CMD ["short"]

