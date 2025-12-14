# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /bin/aws-mock .

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache curl ca-certificates \
	&& adduser -D -u 10001 appuser
COPY --from=build /bin/aws-mock /usr/local/bin/aws-mock
USER appuser
EXPOSE 4566
ENTRYPOINT ["/usr/local/bin/aws-mock"]
