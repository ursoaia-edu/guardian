package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

const minPasswordLen = 8

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "A valid email is required"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		slog.Error("hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: req.Email, PasswordHash: hash, Name: req.Name,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(w, http.StatusConflict, ErrorResponse{Error: "That email is already registered"})
			return
		}
		slog.Error("create user", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	accountName := req.Name
	if accountName == "" {
		accountName = req.Email
	}
	// The user now exists, so scope the transaction to them: the pre-scope
	// INSERT policy on accounts requires owner_user_id = current_user_id(),
	// and the pre-scope SELECT policy the RETURNING clause needs requires the
	// same. Nothing here can create an account owned by anyone else.
	if err := scopeTx(ctx, tx, "app.user_id", user.ID); err != nil {
		slog.Error("scope registration transaction to the user", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	account, err := q.CreateAccount(ctx, db.CreateAccountParams{Name: accountName, OwnerUserID: user.ID})
	if err != nil {
		slog.Error("create account", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	// The account now exists, so scope the rest of this transaction to it. There
	// is no unscoped INSERT policy on account_members precisely so that no code
	// path can make an arbitrary user the owner of an arbitrary account; this
	// insert has to earn its scope like every other write.
	if err := scopeTx(ctx, tx, "app.account_id", account.ID); err != nil {
		slog.Error("scope registration transaction", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	if err := q.AddAccountMember(ctx, db.AddAccountMemberParams{
		AccountID: account.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		slog.Error("add owner membership", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit registration", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not create the account"})
		return
	}

	slog.Info("account registered", "account_id", account.ID)
	writeJSON(w, http.StatusCreated, map[string]string{
		"account_id": account.ID.String(),
		"user_id":    user.ID.String(),
	})
}

const (
	sessionCookieName = "guardian_session"
	sessionTTL        = 30 * 24 * time.Hour
	accountHeader     = "X-Guardian-Account"
)

// dummyPasswordHash is what a login attempt for an unknown email is verified
// against, so that a miss costs the same argon2 work as a hit. Built once at
// startup; the password it encodes is unreachable because no registration path
// can produce it.
var dummyPasswordHash = func() string {
	h, err := hashPassword("no user has this password")
	if err != nil {
		panic("hashing the dummy password failed: " + err.Error())
	}
	return h
}()

// loginRequest is deliberately not registerRequest: login takes a client
// kind, and reusing the registration struct is how a field added to one ends
// up silently accepted by the other.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Client opts into a token in the response body. Empty (the browser
	// cabinet) gets the HttpOnly cookie and nothing else.
	Client string `json:"client"`
}

// clientMobile is the only caller that may receive the session token in the
// response body: a Flutter app cannot use a cookie jar comfortably and stores
// the token in the Keychain/Keystore instead.
const clientMobile = "mobile"

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}
	ctx := r.Context()
	q := db.New(s.pool)

	user, err := q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(req.Email)))
	// A database failure is not a rejected password. Reporting it as one tells
	// every parent their password is wrong during an outage, and hides the
	// outage in the logs behind what looks like credential stuffing.
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("look up user for login", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not sign in"})
		return
	}

	// Unknown email and wrong password must be indistinguishable on the time
	// axis as well as in the response. Short-circuiting past verifyPassword on
	// a miss answers in ~1ms where a real password costs ~100ms, which is an
	// account-enumeration oracle measurable over the internet without
	// statistics. So the argon2 work is always spent, against a dummy hash when
	// there is no user.
	stored := dummyPasswordHash
	if err == nil {
		stored = user.PasswordHash
	}
	if !verifyPassword(stored, req.Password) || err != nil {
		slog.Warn("failed login", "ip", clientIP(r))
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "Invalid email or password"})
		return
	}

	plain, hash := newToken()
	expires := time.Now().Add(sessionTTL)
	if err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hash, UserID: user.ID, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
		Ip: clientIP(r), UserAgent: r.UserAgent(),
	}); err != nil {
		slog.Error("create session", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Could not sign in"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: plain, Path: "/",
		Expires: expires, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode,
	})
	// Returning the token in the body unconditionally cancels the HttpOnly
	// flag that was just set: script on the cabinet's origin could read it
	// out of the login response and keep it somewhere XSS can reach. So the
	// body token is opt-in, and only the client that genuinely cannot use a
	// cookie asks for it.
	if req.Client == clientMobile {
		writeJSON(w, http.StatusOK, map[string]string{"token": plain})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if plain := sessionTokenFrom(r); plain != "" {
		if err := db.New(s.pool).DeleteSession(r.Context(), hashToken(plain)); err != nil {
			slog.Error("delete session", "error", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}
	var accounts []db.ListAccessibleAccountsRow
	err := s.inUser(r.Context(), t.UserID, func(tx pgx.Tx) error {
		var err error
		accounts, err = db.New(tx).ListAccessibleAccounts(r.Context(), t.UserID)
		return err
	})
	if err != nil {
		slog.Error("list accessible accounts", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":    t.UserID.String(),
		"account_id": t.AccountID.String(),
		"role":       t.Role,
		"accounts":   accountResponses(accounts),
	})
}
