FROM node:24.16.0-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/index.html web/src.js web/style.css ./
COPY web/public ./public
RUN npm run build

FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/communication .

FROM alpine:3.24.3
RUN apk add --no-cache ca-certificates && addgroup -g 10001 app && adduser -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build /out/communication /usr/local/bin/communication
COPY --from=web /src/web/dist /app/web
RUN mkdir -p /app/pb_data && chown app:app /app/pb_data
USER app
ENTRYPOINT ["/usr/local/bin/communication"]
CMD ["serve", "--dir=/app/pb_data", "--http=0.0.0.0:8090", "--origins=https://chat.tmadhu.me"]

