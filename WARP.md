# WARP.md

This file provides guidance to WARP (warp.dev) when working with code in this repository.

## Project Overview

ProcSentinel is a process monitoring and control system written in Go. The project is structured as a multi-component system with planned support for agent, server, and mobile components.

### Architecture

- **agent/**: Core monitoring agent that watches system processes and terminates blocked applications
- **server/**: HTTP REST API server for managing blocked applications and system status
- **mobile/**: Flutter mobile client for remote management
- **bin/**: Build output directory for compiled binaries

### Platform Compatibility

The agent is now **cross-platform compatible** and automatically detects the operating system:

- **Windows**: Uses `tasklist` to list processes and `taskkill` to terminate them
- **Linux/macOS**: Uses `ps aux` to list processes and `pkill -f` to terminate them

The application runs natively on Windows, Linux, and macOS without modification.

## Development Commands

### Building the Application

```bash
# Build the agent component
cd agent && go build -o ../bin/procsentinel-agent main.go

# Build the server component
cd server && go build -o ../bin/procsentinel-server main.go

# Or build from root directory
go build -o bin/procsentinel-agent ./agent
go build -o bin/procsentinel-server ./server
```

### Running the Application

```bash
# 1. Start the server first
./bin/procsentinel-server

# 2. In another terminal, start the agent
./bin/procsentinel-agent

# Or run directly with go
cd server && go run main.go  # Start server first
cd agent && go run main.go   # Start agent second
```

### Agent-Server Communication

```bash
# Configure server address (optional)
echo "SERVER_ADDRESS=http://localhost:8080" > .env

# The agent will:
# - Fetch blocked apps immediately on startup
# - Update the list every 60 seconds
# - Use empty list if server is down
# - Log all activities with timestamps
```

### Project Management

```bash
# Initialize go module (if not already done)
go mod init procsentinel

# Tidy dependencies
go mod tidy

# Format code
go fmt ./...

# Vet code for issues
go vet ./...
```

## Key Implementation Details

### Process Monitoring Logic

The agent component implements a cross-platform continuous monitoring loop that:
1. **Server Integration**: Fetches blocked applications list from server every 60 seconds
2. **Environment Configuration**: Reads server address from `.env` file (`SERVER_ADDRESS`)
3. **Fallback Handling**: Uses empty list when server is unavailable
4. **OS Detection**: Detects the operating system using `runtime.GOOS`
5. **Process Listing**: Executes appropriate command to retrieve running processes:
   - Windows: `tasklist`
   - Linux/macOS: `ps aux`
6. **Process Scanning**: Scans output for blocked process names (case-insensitive)
7. **Process Termination**: Terminates matching processes using OS-appropriate commands:
   - Windows: `taskkill /F /IM <process_name>`
   - Linux/macOS: `pkill -f <process_name>`
8. **Monitoring Loop**: Repeats process checking every 1 second

### Agent Configuration

The agent now dynamically fetches blocked applications from the server:

#### Environment Configuration
Create a `.env` file in the project root:
```env
SERVER_ADDRESS=http://localhost:8080
```

#### Server Integration Features
- **Dynamic Updates**: Fetches blocked applications every 60 seconds
- **Immediate Sync**: Initial fetch on startup (no 60-second delay)
- **Graceful Degradation**: Uses empty list when server is unavailable
- **HTTP Timeout**: 10-second timeout for server requests
- **Background Updates**: Non-blocking goroutine for server communication

#### Configuration Priority
1. `.env` file settings
2. Environment variables
3. Default fallback (`http://localhost:8080`)

## Server Component

### API Endpoints

The server provides a REST API for managing blocked applications:

- **GET /applications** - Get list of blocked applications
- **POST /applications** - Add new blocked application
- **DELETE /applications/{name}** - Remove blocked application
- **DELETE /reset** - Remove all blocked applications
- **GET /status** - Get server status (enabled/disabled)
- **PUT /status** - Update server status
- **GET /health** - Health check endpoint

### Server Features

- **Persistent Storage**: Uses SQLite database for data persistence across restarts
- **In-Memory Caching**: Fast read operations with write-through cache synchronization
- **Thread-safe**: Uses mutex for concurrent access to cached data
- **Status control**: Can be enabled/disabled, returns empty list when disabled
- **JSON responses**: All endpoints return proper JSON with appropriate HTTP status codes
- **Error handling**: Comprehensive error responses for invalid requests
- **Default port**: Runs on port 8080
- **Database file**: Stores data in `./server/procsentinel.db` SQLite file

### API Documentation

Comprehensive API documentation is available in the **Swagger/OpenAPI specification**:
- **File**: `swagger.yaml`
- **Interactive docs**: Use tools like Swagger UI or Postman to import the specification
- **Client generation**: Generate client SDKs for various programming languages
- **Local viewing**: Open `swagger-ui.html` in a browser (requires server running for "Try it out" functionality)

### API Usage Examples

```bash
# Get server status
curl http://localhost:8080/status

# Get blocked applications list
curl http://localhost:8080/applications

# Add application to blocked list
curl -X POST http://localhost:8080/applications \
  -H "Content-Type: application/json" \
  -d '{"name":"firefox"}'

# Remove application from blocked list
curl -X DELETE http://localhost:8080/applications/firefox

# Remove all applications from blocked list
curl -X DELETE http://localhost:8080/reset

# Disable server (returns empty applications list)
curl -X PUT http://localhost:8080/status \
  -H "Content-Type: application/json" \
  -d '{"enabled":false}'

# Re-enable server
curl -X PUT http://localhost:8080/status \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

### Storage Architecture

The server uses a **hybrid storage approach** combining SQLite persistence with in-memory caching:

#### Database Schema
```sql
-- Blocked applications table
CREATE TABLE blocked_applications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Server status table (single row)
CREATE TABLE server_status (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT 1,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

#### Caching Strategy
- **Write-through cache**: All writes update both cache and database
- **Cache-first reads**: Read operations use in-memory cache for speed
- **Startup loading**: Cache is populated from database on server startup
- **Data consistency**: Mutex ensures cache and database stay synchronized

#### Benefits
- **Fast reads**: In-memory cache provides millisecond response times
- **Data persistence**: SQLite ensures data survives server restarts
- **Reliability**: Atomic database operations prevent data corruption
- **Simplicity**: No external database dependencies

## Mobile Client

### Flutter Application

The mobile directory contains a Flutter application for remote management of the ProcSentinel system.

#### Features
- **Server Configuration**: Input field for server address with validation
- **Connection Testing**: Test connectivity to the ProcSentinel server
- **Dashboard**: View blocked applications and server status
- **Application Management**: Add, remove, and reset blocked applications
- **Server Control**: Enable/disable server functionality remotely
- **Persistent Settings**: Server address saved locally using SharedPreferences

#### Setup and Development

```bash
# Navigate to mobile directory
cd mobile

# Get dependencies
flutter pub get

# Run on connected device or emulator
flutter run

# Build for release (Android)
flutter build apk

# Build for release (iOS)
flutter build ios
```

#### Mobile App Structure

- **lib/main.dart**: Main app entry point with navigation
- **lib/screens/**: UI screens (home dashboard, settings)
- **lib/services/**: API communication and settings management
- **lib/models/**: Data models (if needed for future expansion)

#### Server Configuration

The mobile app connects to the ProcSentinel server using:
1. Settings screen with server address input
2. Validation (must start with http:// or https://)
3. Connection testing with /health endpoint
4. Persistent storage of server address

### Project Structure

This project now includes a complete system with agent, server, and mobile client components.

## Development Notes

- The project uses Go 1.25.1
- No external dependencies currently required
- No tests are currently implemented
- **Cross-platform compatible**: Works on Windows, Linux, and macOS
- **Development environment**: Tested on Linux Mint with zsh shell
- Build output is placed in `bin/` directory (should be added to `.gitignore`)
- Uses Go's `runtime.GOOS` for automatic OS detection
- **Server component**: HTTP REST API on port 8080 for managing blocked applications
- **SQLite storage**: Persistent storage using SQLite database with in-memory caching
- **Database location**: `./server/procsentinel.db` file in the server directory
- **Agent-Server Integration**: Agent fetches blocked apps from server every 60 seconds
- **Environment Configuration**: Uses `.env` file for server address configuration
- **Graceful Degradation**: Agent continues working even when server is unavailable
