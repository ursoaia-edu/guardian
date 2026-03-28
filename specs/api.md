# API Reference

Base URL: `http://<host>:8080`

All authenticated endpoints require: `Authorization: Bearer <token>`

---

## Unauthenticated

### `GET /health`

Health check for load balancers and monitoring.

**Response** `200`
```json
{
  "status": "ok"
}
```

---

## Client Endpoints (TOKEN auth)

### `GET /client/sync`

Returns the full state for the agent: applications filtered by current server mode, server mode, and client entries.

If the server is disabled or the computer is unblocked (`blocked: false`), `applications` and `client` are empty arrays.

**Query params:**
- `identity` (optional) — computer ID integer

**Response** `200`
```json
{
  "applications": [
    {
      "name": "firefox",
      "mode": "blacklist"
    },
    {
      "name": "chrome",
      "mode": "blacklist"
    }
  ],
  "mode": "blacklist",
  "client": [
    {
      "name": "power",
      "status": true
    }
  ]
}
```

**Response** `200` (server disabled or computer unblocked)
```json
{
  "applications": [],
  "mode": "blacklist",
  "client": []
}
```

---

## Management Endpoints (ADMIN_TOKEN auth)

### `GET /manage/applications`

Returns all applications regardless of server status.

**Response** `200`
```json
{
  "applications": [
    {
      "id": 1,
      "name": "firefox",
      "enabled": true,
      "mode": "blacklist"
    },
    {
      "id": 2,
      "name": "chrome",
      "enabled": true,
      "mode": "whitelist"
    }
  ]
}
```

---

### `POST /manage/applications`

Add a new application.

**Request**
```json
{
  "name": "firefox",
  "mode": "blacklist"
}
```

`mode` is optional, defaults to `"blacklist"`. Accepted values: `"blacklist"`, `"whitelist"`.

**Response** `201`
```json
{
  "message": "Application 'firefox' added"
}
```

**Response** `400`
```json
{
  "error": "Application name cannot be empty"
}
```

---

### `PUT /manage/applications`

Update an application's enabled state and/or mode.

**Request**
```json
{
  "name": "firefox",
  "enabled": false,
  "mode": "whitelist"
}
```

`enabled` accepts `true`, `false`, `0`, or `1`. `mode` is optional — only updated if provided.

**Response** `200`
```json
{
  "message": "Application 'firefox' updated"
}
```

**Response** `404`
```json
{
  "error": "Application 'unknown' not found"
}
```

---

### `DELETE /manage/applications`

Remove an application.

**Request**
```json
{
  "name": "firefox"
}
```

**Response** `200`
```json
{
  "message": "Application 'firefox' removed"
}
```

**Response** `404`
```json
{
  "error": "Application 'unknown' not found"
}
```

---

### `DELETE /manage/applications/reset`

Remove all applications.

**Response** `200`
```json
{
  "message": "Reset complete: removed 5 applications"
}
```

---

### `GET /status`

Get server enabled state and mode.

**Response** `200`
```json
{
  "enabled": true,
  "mode": "blacklist"
}
```

---

### `PUT /status`

Update server enabled state and/or mode.

**Request**
```json
{
  "enabled": true,
  "mode": "whitelist"
}
```

`mode` is optional — only updated if provided. Accepted values: `"blacklist"`, `"whitelist"`.

**Response** `200`
```json
{
  "message": "Server enabled (mode: whitelist)"
}
```

**Response** `400`
```json
{
  "error": "Mode must be 'blacklist' or 'whitelist'"
}
```

---

### `GET /info`

Get server information.

**Response** `200`
```json
{
  "server_ip": "192.168.1.10",
  "server_port": "8080",
  "version": "1.0.0",
  "status": "enabled",
  "mode": "blacklist"
}
```

---

### `GET /client`

Get all client entries.

**Response** `200`
```json
{
  "entries": [
    {
      "name": "power",
      "status": true
    }
  ]
}
```

---

### `PUT /client`

Create or update a client entry.

**Request**
```json
{
  "name": "power",
  "status": false
}
```

**Response** `200`
```json
{
  "message": "Client 'power' disabled"
}
```

---

### `GET /manage/computers`

Get all registered computers.

**Response** `200`
```json
{
  "computers": [
    {
      "identity": 1,
      "blocked": true,
      "datetime": "2026-03-28 01:03:10"
    },
    {
      "identity": 2,
      "blocked": false,
      "datetime": "2026-03-28 01:05:22"
    }
  ],
  "current_time": "2026-03-28T01:10:00+02:00"
}
```

---

### `PUT /manage/computers`

Update a computer's blocked status.

**Request**
```json
{
  "identity": 1,
  "blocked": true
}
```

**Response** `200`
```json
{
  "message": "Computer 1 blocked"
}
```

---

### `DELETE /manage/computers/reset`

Unblock all computers.

**Response** `200`
```json
{
  "message": "All computers unblocked"
}
```

---

### `PUT /manage/computers/block_all`

Block all computers.

**Response** `200`
```json
{
  "message": "All computers blocked"
}
```

---

## Error Responses

All endpoints return errors in this format:

```json
{
  "error": "Description of the error"
}
```

Common HTTP status codes:
- `400` — Invalid request (bad JSON, missing fields, invalid mode)
- `401` — Unauthorized (missing or wrong token)
- `404` — Resource not found
- `500` — Internal server error
