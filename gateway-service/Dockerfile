FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /gateway-service .

FROM scratch
COPY --from=build /gateway-service /gateway-service
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway-service"]
