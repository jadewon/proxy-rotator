FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/proxy-rotator ./cmd/proxy-rotator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/proxy-rotator /proxy-rotator
EXPOSE 3128 8080
USER nonroot:nonroot
ENTRYPOINT ["/proxy-rotator"]
