FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /payment-service .

FROM scratch
COPY --from=build /payment-service /payment-service
USER 65532:65532
EXPOSE 8060
ENTRYPOINT ["/payment-service"]
