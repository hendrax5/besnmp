# Stage 1: Build the frontend
FROM node:18-alpine AS frontend
WORKDIR /app
COPY internal/site/package*.json ./internal/site/
RUN cd internal/site && npm ci || npm install
COPY internal/site ./internal/site
RUN cd internal/site && npm run build

# Stage 2: Build the backend (Golang)
FROM golang:1.22-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy the built site from frontend stage
COPY --from=frontend /app/internal/site/dist ./internal/site/dist
# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/beszel-hub ./internal/cmd/hub

# Stage 3: Final lightweight image
FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=backend /app/beszel-hub ./
EXPOSE 8090
VOLUME [ "/beszel_data" ]

# Start the embedded PocketBase server
CMD [ "./beszel-hub", "serve", "--http=0.0.0.0:8090", "--dir=/beszel_data" ]
