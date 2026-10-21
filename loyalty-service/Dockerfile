FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /loyalty-service .

FROM scratch
COPY --from=build /loyalty-service /loyalty-service
USER 65532:65532
EXPOSE 8050
ENTRYPOINT ["/loyalty-service"]
