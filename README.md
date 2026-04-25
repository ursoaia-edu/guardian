<div align="center">

<img src="mobile/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher_foreground.webp" alt="Guardian logo" width="160" />

# Guardian

**Process monitoring and control system — block or whitelist applications across Windows machines, manage them remotely from an Android app, all coordinated through a central server.**

[![License](https://img.shields.io/badge/license-MIT-3b82f6?style=flat-square)](LICENSE)
[![Server](https://img.shields.io/badge/server-Go-00ADD8?style=flat-square&logo=go&logoColor=white)](server/)
[![Agent](https://img.shields.io/badge/agent-Windows-0078D6?style=flat-square&logo=windows&logoColor=white)](agent/)
[![Mobile](https://img.shields.io/badge/mobile-Android-3DDC84?style=flat-square&logo=android&logoColor=white)](mobile/)
[![Built with](https://img.shields.io/badge/built%20with-Go_%7C_Flutter-02569B?style=flat-square&logo=flutter&logoColor=white)](https://flutter.dev)

[**Quick Start**](#quick-start) · [Architecture](#architecture) · [API Reference](#api-reference) · [Issues](../../issues)

</div>

<br />


## Quick Start

Pre-built binaries are available in the `dist/` directory:

```
dist/
├── server/
│   ├── guardian-server              # Server binary (Linux)
│   ├── server.env                   # Server config template
│   ├── install.sh                   # Install script
│   └── docker-compose.yml           # Docker Compose config
├── guardian.apk                     # Mobile app (Android)
└── agent/
    ├── agent.env                    # Agent config template
    ├── Install-Guardian.bat         # Auto-detect arch and install agent
    ├── Uninstall-Guardian.bat       # Auto-detect arch and uninstall agent
```

### 1. Server (Linux)

Copy the `dist/server/` folder to your server. Rename the env file and configure it:

```bash
cp server.env .env
```

```env
SERVER_ADDRESS=http://0.0.0.0:8080
TOKEN=your_token_here
ADMIN_TOKEN=your_admin_token_here
```
## Running the Server

**Option A: Run directly**

```bash
./guardian-server
```

**Option B: Install as systemd service**

```bash
sudo ./install.sh
```

**Option C: Docker**

```bash
docker compose up -d
```

The `.env` file is optional with Docker — you can pass environment variables directly via `docker-compose.yml` or `docker run --env-file`.

### 2. Agent (Windows)

Edit `agent.env` with your server details:

```env
SERVER_ADDRESS=your_server_address
TOKEN=your_token_here
```

Double-click `Install-Guardian.bat` (auto-detects 32/64-bit and runs the appropriate installer as admin). The installer will prompt for an optional `IDENTITY` number to register the computer with the server.

To uninstall, double-click `Uninstall-Guardian.bat`.

### 3. Mobile App (Android)

Install `guardian.apk` on your Android device. Open the app, go to Settings, and configure the server address and admin token.

## Architecture

```
+-----------+         +--------+         +-------+
|  Mobile   | ------> | Server | <------ | Agent |
| (Android) |  Admin  | (Linux)|  Client | (Win) |
+-----------+  Token   +--------+  Token  +-------+
                          |
                       [SQLite]
```

- **Server** -- Go REST API with SQLite. Manages blocked applications, modes, computer registrations, and client entries. Runs as a Linux systemd service.
- **Agent** -- Go process monitor. Polls the server for its configuration, scans running processes every second, and kills matches. Runs as a Windows service (also supports Linux/macOS).
- **Mobile** -- Flutter Android app. Provides a UI to manage blocked apps, toggle modes, control computers, and trigger system actions like remote shutdown.

## Modes

| Mode | Behavior |
|---|---|
| **blacklist** | Only processes in the block list are killed |
| **whitelist** | All processes NOT in the allow list are killed (system processes excluded) |
| **free** | No processes are killed (returned when server is disabled or computer is unblocked) |

## API Reference

All endpoints except `/health` require a `Authorization: Bearer <token>` header.

### Client (TOKEN auth)

| Method | Endpoint | Description |
|---|---|---|
| GET | `/client/sync?identity={id}` | Agent sync -- returns applications, mode, and client entries |

### Admin (ADMIN_TOKEN auth)

**Applications**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/manage/applications` | List all applications |
| POST | `/manage/applications` | Add application (name, mode) |
| PUT | `/manage/applications` | Update application (name, enabled, mode) |
| DELETE | `/manage/applications` | Remove application (name, mode) |
| DELETE | `/manage/applications/reset` | Clear all applications |

**Status**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/status` | Get server enabled state and mode |
| PUT | `/status` | Update enabled state and/or mode |
| GET | `/info` | Server info (IP, port, version, status) |

**Client Entries**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/client` | List all client entries |
| PUT | `/client` | Update a client entry (name, status) |

**Computers**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/manage/computers` | List computers with block status and last seen |
| PUT | `/manage/computers` | Set computer blocked status (identity, blocked) |
| DELETE | `/manage/computers/reset` | Unblock all computers |
| PUT | `/manage/computers/block_all` | Block all computers |

**Unauthenticated**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/health` | Health check |

## Remote Shutdown

Setting the `power` client entry to `false` via the `/client` endpoint triggers a system shutdown on all connected agents. On Windows, this uses the `InitiateSystemShutdownExW` API to force-close applications and power off the machine.

## Configuration

### Server `.env`

| Variable | Description | Example |
|---|---|---|
| `SERVER_ADDRESS` | Listen address | `http://0.0.0.0:8080` |
| `TOKEN` | Client/agent auth token | `your_token_here` |
| `ADMIN_TOKEN` | Admin/mobile auth token | `your_admin_token_here` |

### Agent `.env`

| Variable | Description | Example |
|---|---|---|
| `SERVER_ADDRESS` | Server URL | `http://192.168.1.10:8080` |
| `TOKEN` | Client auth token (must match server) | `your_token_here` |

## Build Requirements

- **Go 1.25+** for server and agent
- **Flutter 3.x** for the mobile app
- Agent Windows builds require PowerShell and `CGO_ENABLED=1`
