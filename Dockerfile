FROM golang:1.14
ENV workdir /build
WORKDIR $workdir
COPY . .

RUN go generate ./... && go install -v ./cmd/short

VOLUME ["/data"]
WORKDIR /data
CMD ["short"]

