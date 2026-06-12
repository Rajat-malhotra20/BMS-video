FROM golang:1.22-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod ./
COPY backend/*.go ./
RUN go build -trimpath -ldflags="-s -w" -o /out/mediamtx-console

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/mediamtx-console /app/mediamtx-console
COPY frontend /app/frontend
EXPOSE 8080
ENTRYPOINT ["/app/mediamtx-console"]
