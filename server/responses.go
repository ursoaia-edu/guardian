package main

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// The API's own types.
//
// Handlers used to return sqlc row structs verbatim, which made the database
// schema the wire contract: every column added to a table appeared in the API
// by default, and keeping one out of a response took a json:"-" tag on
// generated code. Both tags that exist — computers.token_hash and
// users.password_hash — were added after a review caught the leak, which is
// the argument for this file. A field is public here because it was written
// here: the same allow-list reasoning the role check uses, applied to data
// leaving the process.
//
// account_id is absent from every type below on purpose. A client never names
// an account (see specs/server.md), and echoing one back is how that starts.

// ts renders a NOT NULL timestamp. A zero value can only mean the column was
// null, which for these columns the schema forbids.
func ts(t pgtype.Timestamptz) time.Time { return t.Time }

// tsPtr renders a nullable timestamp as null rather than as year zero.
func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

type RoomResponse struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	Mode              string    `json:"mode"`
	ProtectionEnabled bool      `json:"protection_enabled"`
	PowerAllowed      bool      `json:"power_allowed"`
	CreatedAt         time.Time `json:"created_at"`
}

func newRoomResponse(r db.Room) RoomResponse {
	return RoomResponse{
		ID: r.ID, Name: r.Name, Mode: r.Mode,
		ProtectionEnabled: r.ProtectionEnabled, PowerAllowed: r.PowerAllowed,
		CreatedAt: ts(r.CreatedAt),
	}
}

func roomResponses(rows []db.Room) []RoomResponse {
	out := make([]RoomResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, newRoomResponse(r))
	}
	return out
}

type ApplicationResponse struct {
	ID        uuid.UUID `json:"id"`
	RoomID    uuid.UUID `json:"room_id"`
	Name      string    `json:"name"`
	List      string    `json:"list"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func newApplicationResponse(a db.Application) ApplicationResponse {
	return ApplicationResponse{
		ID: a.ID, RoomID: a.RoomID, Name: a.Name, List: a.List,
		Enabled: a.Enabled, CreatedAt: ts(a.CreatedAt),
	}
}

func applicationResponses(rows []db.Application) []ApplicationResponse {
	out := make([]ApplicationResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, newApplicationResponse(a))
	}
	return out
}

// ComputerResponse omits token_hash — the machine's credential digest — by
// construction rather than by a tag on generated code.
type ComputerResponse struct {
	ID           uuid.UUID       `json:"id"`
	RoomID       *uuid.UUID      `json:"room_id"`
	DisplayName  string          `json:"display_name"`
	MachineGUID  string          `json:"machine_guid"`
	Hostname     string          `json:"hostname"`
	OSName       string          `json:"os_name"`
	OSBuild      string          `json:"os_build"`
	Arch         string          `json:"arch"`
	AgentVersion string          `json:"agent_version"`
	Hardware     json.RawMessage `json:"hardware"`
	Runtime      json.RawMessage `json:"runtime"`
	Blocked      bool            `json:"blocked"`
	EnrolledAt   time.Time       `json:"enrolled_at"`
	LastSeenAt   *time.Time      `json:"last_seen_at"`
}

func newComputerResponse(c db.Computer) ComputerResponse {
	return ComputerResponse{
		ID: c.ID, RoomID: c.RoomID, DisplayName: c.DisplayName,
		MachineGUID: c.MachineGuid, Hostname: c.Hostname, OSName: c.OsName,
		OSBuild: c.OsBuild, Arch: c.Arch, AgentVersion: c.AgentVersion,
		Hardware: c.Hardware, Runtime: c.Runtime, Blocked: c.Blocked,
		EnrolledAt: ts(c.EnrolledAt), LastSeenAt: tsPtr(c.LastSeenAt),
	}
}

func computerResponses(rows []db.Computer) []ComputerResponse {
	out := make([]ComputerResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, newComputerResponse(c))
	}
	return out
}

// MemberResponse describes a room guest. ID is the user's id — the value
// DELETE /rooms/{roomID}/members/{userID} takes — and keeps the field name the
// API already published: these types exist to stop the schema leaking into the
// wire format, not to renegotiate it.
type MemberResponse struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

func memberResponses(rows []db.ListRoomMembersRow) []MemberResponse {
	out := make([]MemberResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, MemberResponse{ID: m.ID, Email: m.Email, Name: m.Name})
	}
	return out
}

type EventResponse struct {
	ID         uuid.UUID       `json:"id"`
	RoomID     *uuid.UUID      `json:"room_id"`
	ComputerID *uuid.UUID      `json:"computer_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

func eventResponses(rows []db.Event) []EventResponse {
	out := make([]EventResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, EventResponse{
			ID: e.ID, RoomID: e.RoomID, ComputerID: e.ComputerID,
			Type: e.Type, Payload: e.Payload, CreatedAt: ts(e.CreatedAt),
		})
	}
	return out
}

type AccountResponse struct {
	AccountID uuid.UUID `json:"account_id"`
	Role      string    `json:"role"`
}

func accountResponses(rows []db.ListAccessibleAccountsRow) []AccountResponse {
	out := make([]AccountResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, AccountResponse{AccountID: a.AccountID, Role: a.Role})
	}
	return out
}
