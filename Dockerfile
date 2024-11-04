# Build stage
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build-env

# Set the working directory for the build
WORKDIR /src

# Copy Go modules and source code
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go program
RUN go mod tidy -compat=1.23 && \
    go build -o centralized-sequencer /src/cmd/centralized-sequencer/main.go

# Final image
FROM alpine:3.18.3

# Set the working directory
WORKDIR /root

# Copy the binary from the build stage
COPY --from=build-env /src/centralized-sequencer /usr/bin/local-sequencer

# Expose the application's port
EXPOSE 50051

# Define arguments as environment variables, which can be set when running the container
ENV HOST=localhost
ENV PORT=50051
ENV LISTEN_ALL=false
ENV BATCH_TIME=2s
ENV DA_ADDRESS=http://localhost:26658
ENV DA_NAMESPACE=""
ENV DA_AUTH_TOKEN=""
ENV DB_PATH="~/.centralized-sequencer-db"
ENV ROLLUP_ID="rollupId"

# Run the application with environment variables passed as flags
CMD ["sh", "-c", "/usr/bin/local-sequencer -host $HOST -port $PORT -listen-all=$LISTEN_ALL -batch-time $BATCH_TIME -da_address $DA_ADDRESS -da_namespace $DA_NAMESPACE -da_auth_token $DA_AUTH_TOKEN -db_path $DB_PATH"]