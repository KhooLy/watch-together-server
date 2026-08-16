FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/watch-together-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/watch-together-server /watch-together-server
EXPOSE 8787
USER nonroot:nonroot
ENTRYPOINT ["/watch-together-server"]
