FROM golang:1.26 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/wuu2 .

FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /app
COPY --from=build /out/wuu2 /app/wuu2

ENV ADDRESS=:8080
ENV UPDATE_INTERVAL_MINUTES=30m
ENV TRAKT_ENABLED=false
ENV BATTLENET_ENABLED=false
ENV APPLEMUSIC_ENABLED=false
ENV STEAM_ENABLED=false

EXPOSE 8080

ENTRYPOINT ["/app/wuu2"]
