// Package auth authenticates people (team members with sessions) and
// machines (API keys), and authorizes each request by module and level.
// Every store has unlimited seats; each member gets a level per module.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"

	"github.com/helloghc/apexx/internal/store"
)

// Modules of the platform.
var Modules = []string{"catalog", "pricing", "inventory", "promotions", "orders", "customers", "payments", "builder", "team", "settings", "reports", "marketplace"}

// Levels in increasing order.
const (
	None  = "none"
	Read  = "read"
	Edit  = "edit"
	Admin = "admin"
)

var rank = map[string]int{None: 0, Read: 1, Edit: 2, Admin: 3}

// ValidLevel reports whether l is a known level.
func ValidLevel(l string) bool { _, ok := rank[l]; return ok }

// Permissions maps module to level.
type Permissions map[string]string

// Allows reports whether p grants at least level on module.
func (p Permissions) Allows(module, level string) bool {
	return rank[p[module]] >= rank[level]
}

// All grants level on every module.
func All(level string) Permissions {
	p := Permissions{}
	for _, m := range Modules {
		p[m] = level
	}
	return p
}

// Validate checks module names and levels.
func (p Permissions) Validate() error {
	for m, l := range p {
		ok := false
		for _, x := range Modules {
			ok = ok || x == m
		}
		if !ok {
			return fmt.Errorf("módulo desconocido %q; usa: %s", m, strings.Join(Modules, ", "))
		}
		if !ValidLevel(l) {
			return fmt.Errorf("nivel %q no válido para %s; usa none, read, edit o admin", l, m)
		}
	}
	return nil
}

// RolePermissions maps API key roles to permissions.
func RolePermissions(role string) Permissions {
	switch role {
	case "owner", "admin":
		return All(Admin)
	case "editor":
		p := All(Edit)
		p["team"], p["payments"], p["settings"] = Read, Read, Read
		return p
	case "publisher":
		p := All(Read)
		p["builder"] = Admin
		return p
	case "viewer":
		return All(Read)
	case "storefront":
		return Permissions{"catalog": Read, "pricing": Read, "inventory": Read}
	}
	return Permissions{}
}

// Principal is the authenticated caller.
type Principal struct {
	Tenant      string      `json:"tenant"`
	Kind        string      `json:"kind"` // member | api_key | app
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Email       string      `json:"email,omitempty"`
	Owner       bool        `json:"owner"`
	Permissions Permissions `json:"permissions"`
	Storefront  bool        `json:"storefront"`         // publishable key: may place orders
	SessionID   string      `json:"-"`                  // session of a signed-in member
	SellerID    string      `json:"sellerId,omitempty"` // marketplace seller this person works for: they only see the seller portal
}

// Actor is the audit-log name of the principal.
func (p Principal) Actor() string { return p.Kind + ":" + p.ID }

type ctxKey struct{}

// With stores p in ctx.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal of ctx.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// ---------- passwords ----------

// HashPassword returns an argon2id PHC string.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, 2, 64*1024, 2, 32)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=2$%s$%s", enc.EncodeToString(salt), enc.EncodeToString(h)), nil
}

// CheckPassword verifies pw against a PHC string in constant time.
func CheckPassword(phc, pw string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[4])
	want, err2 := enc.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidatePassword enforces a minimum policy.
func ValidatePassword(pw string) error {
	if len(pw) < 12 {
		return errors.New("la contraseña debe tener al menos 12 caracteres")
	}
	return nil
}

// ---------- service ----------

// Service authenticates and manages members.
type Service struct {
	DB         *store.DB
	SessionTTL time.Duration
	Keys       Cipher           // encrypts two-step verification secrets; nil = 2FA unavailable
	Now        func() time.Time // tests
}

// Errors.
var (
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	ErrBadCredentials  = errors.New("auth: invalid email or password")
	ErrNotFound        = errors.New("auth: not found")
	ErrLastOwner       = errors.New("auth: the store must keep at least one active owner")
)

// Member is a team member (unlimited per store).
type Member struct {
	ID          string      `json:"id"`
	Email       string      `json:"email"`
	Name        string      `json:"name"`
	Owner       bool        `json:"owner"`
	Status      string      `json:"status"`
	Permissions Permissions `json:"permissions"`
	CreatedAt   time.Time   `json:"createdAt"`
	LastLoginAt *time.Time  `json:"lastLoginAt"`
	TwoStep     bool        `json:"twoStep"` // two-step verification is on
	SellerID    string      `json:"sellerId,omitempty"`
}

func newID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// CreateMember adds a member. Owners get admin on every module.
func (s *Service) CreateMember(ctx context.Context, tenant, actor, email, name, password string, perms Permissions, owner bool) (Member, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return Member{}, errors.New("correo inválido")
	}
	if err := ValidatePassword(password); err != nil {
		return Member{}, err
	}
	if owner {
		perms = All(Admin)
	}
	if perms == nil {
		perms = Permissions{}
	}
	if err := perms.Validate(); err != nil {
		return Member{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Member{}, err
	}
	m := Member{ID: newID("mem_", 8), Email: email, Name: name, Owner: owner, Status: "active", Permissions: perms, CreatedAt: time.Now().UTC()}
	pj, _ := json.Marshal(perms)
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO members (tenant_id, id, email, name, password_hash, permissions, is_owner) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, m.ID, email, name, hash, pj, owner); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "member.create", m.ID, map[string]any{"email": email, "permissions": perms, "owner": owner})
	})
	return m, err
}

// Members lists the team.
func (s *Service) Members(ctx context.Context, tenant string) ([]Member, error) {
	var out []Member
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, email, name, is_owner, status, permissions, created_at, last_login_at, totp_enabled_at IS NOT NULL, COALESCE(seller_id,'') FROM members WHERE tenant_id=$1 ORDER BY created_at`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanMember)
		return err
	})
	return out, err
}

func scanMember(r pgx.CollectableRow) (Member, error) {
	var m Member
	var pj []byte
	err := r.Scan(&m.ID, &m.Email, &m.Name, &m.Owner, &m.Status, &pj, &m.CreatedAt, &m.LastLoginAt, &m.TwoStep, &m.SellerID)
	_ = json.Unmarshal(pj, &m.Permissions)
	return m, err
}

// UpdateMember changes permissions and/or status. Disabling revokes sessions.
func (s *Service) UpdateMember(ctx context.Context, tenant, actor, id string, perms Permissions, status string) (Member, error) {
	if perms != nil {
		if err := perms.Validate(); err != nil {
			return Member{}, err
		}
	}
	if status != "" && status != "active" && status != "disabled" {
		return Member{}, errors.New("status debe ser active o disabled")
	}
	var m Member
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, email, name, is_owner, status, permissions, created_at, last_login_at, totp_enabled_at IS NOT NULL, COALESCE(seller_id,'') FROM members WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id)
		if err != nil {
			return err
		}
		m, err = pgx.CollectExactlyOneRow(rows, scanMember)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if m.Owner && status == "disabled" {
			var owners int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM members WHERE tenant_id=$1 AND is_owner AND status='active'`, tenant).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
		if perms != nil && !m.Owner {
			m.Permissions = perms
		}
		if status != "" {
			m.Status = status
		}
		pj, _ := json.Marshal(m.Permissions)
		if _, err := tx.Exec(ctx, `UPDATE members SET permissions=$3, status=$4 WHERE tenant_id=$1 AND id=$2`, tenant, id, pj, m.Status); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "member.update", id, map[string]any{"permissions": m.Permissions, "status": m.Status})
	})
	if err == nil && m.Status == "disabled" {
		_, err = s.DB.System.Exec(ctx, `DELETE FROM sessions WHERE tenant_id=$1 AND member_id=$2`, tenant, id)
	}
	return m, err
}

// Login checks credentials and creates a session; returns the opaque token.
// A member with two-step verification must also send code (from the app, or a
// recovery code): ErrTOTPRequired asks for it, ErrBadCode rejects it.
func (s *Service) Login(ctx context.Context, tenant, email, password, code, ip, ua string) (string, Member, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var m Member
	var hash string
	var pj []byte
	err := s.DB.System.QueryRow(ctx, `SELECT id, email, name, is_owner, status, permissions, password_hash, totp_enabled_at IS NOT NULL FROM members WHERE tenant_id=$1 AND email=$2`, tenant, email).
		Scan(&m.ID, &m.Email, &m.Name, &m.Owner, &m.Status, &pj, &hash, &m.TwoStep)
	if err != nil {
		// Burn the same time as a real check so timing does not reveal accounts.
		CheckPassword("$argon2id$v=19$m=65536,t=2,p=2$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", password)
		return "", Member{}, ErrBadCredentials
	}
	if m.Status != "active" || !CheckPassword(hash, password) {
		return "", Member{}, ErrBadCredentials
	}
	if m.TwoStep {
		if err := s.checkSecondStep(ctx, tenant, m.ID, code); err != nil {
			return "", Member{}, err
		}
	}
	_ = json.Unmarshal(pj, &m.Permissions)
	token := "aps_" + newID("", 32)
	sum := sha256.Sum256([]byte(token))
	ttl := s.SessionTTL
	if ttl == 0 {
		ttl = 12 * time.Hour
	}
	_, err = s.DB.System.Exec(ctx, `INSERT INTO sessions (token_hash, tenant_id, member_id, expires_at, ip, user_agent, id) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		sum[:], tenant, m.ID, time.Now().Add(ttl), ip, truncate(ua, 200), newID("ses_", 8))
	if err != nil {
		return "", Member{}, err
	}
	_, _ = s.DB.System.Exec(ctx, `UPDATE members SET last_login_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, m.ID)
	return token, m, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Logout deletes a session.
func (s *Service) Logout(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	_, err := s.DB.System.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, sum[:])
	return err
}

// Authenticate resolves a bearer token: "aps_" session or "apx_" API key.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	switch {
	case strings.HasPrefix(token, "aps_"):
		sum := sha256.Sum256([]byte(token))
		var p Principal
		var pj []byte
		var seen time.Time
		err := s.DB.System.QueryRow(ctx, `SELECT m.tenant_id, m.id, m.name, m.email, m.is_owner, m.permissions, s.id, s.last_seen_at, COALESCE(m.seller_id,'')
			FROM sessions s JOIN members m ON m.tenant_id=s.tenant_id AND m.id=s.member_id
			WHERE s.token_hash=$1 AND s.expires_at > now() AND m.status='active'`, sum[:]).
			Scan(&p.Tenant, &p.ID, &p.Name, &p.Email, &p.Owner, &pj, &p.SessionID, &seen, &p.SellerID)
		if err != nil {
			return Principal{}, ErrUnauthenticated
		}
		if time.Since(seen) > time.Minute { // "last seen" is refreshed at most once a minute
			_, _ = s.DB.System.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE token_hash=$1`, sum[:])
		}
		_ = json.Unmarshal(pj, &p.Permissions)
		if p.Owner {
			p.Permissions = All(Admin)
		}
		if p.SellerID != "" {
			// A seller's person never gets store permissions, whatever the row says.
			p.Owner, p.Permissions = false, Permissions{}
		}
		p.Kind = "member"
		return p, nil
	case strings.HasPrefix(token, "apa_"):
		tenant, a, err := s.DB.LookupApp(ctx, token)
		if err != nil {
			return Principal{}, ErrUnauthenticated
		}
		perms := Permissions{}
		for m, l := range a.Permissions {
			if m != "team" && rank[l] > 0 && rank[l] <= rank[Admin] {
				perms[m] = l // an app never manages the team, whatever is stored
			}
		}
		return Principal{Tenant: tenant, Kind: "app", ID: a.ID, Name: a.Name, Permissions: perms}, nil
	case strings.HasPrefix(token, "apx_"):
		k, err := s.DB.LookupAPIKey(ctx, token)
		if err != nil {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{Tenant: k.Tenant, Kind: "api_key", ID: k.KeyID, Name: k.Name,
			Permissions: RolePermissions(k.Role), Storefront: k.Role == "storefront", Owner: k.Role == "owner"}, nil
	}
	return Principal{}, ErrUnauthenticated
}

// PurgeSessions deletes expired sessions.
func (s *Service) PurgeSessions(ctx context.Context) error {
	_, err := s.DB.System.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// SetPassword replaces a member's password and ends all of their sessions.
// When current is not empty it must match (self-service change).
func (s *Service) SetPassword(ctx context.Context, tenant, actor, memberID, current, next string) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	if current != "" {
		var hash string
		if err := s.DB.System.QueryRow(ctx, `SELECT password_hash FROM members WHERE tenant_id=$1 AND id=$2`, tenant, memberID).Scan(&hash); err != nil || !CheckPassword(hash, current) {
			return ErrBadCredentials
		}
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE members SET password_hash=$3 WHERE tenant_id=$1 AND id=$2`, tenant, memberID, hash)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "member.password", memberID, nil)
	})
	if err != nil {
		return err
	}
	_, err = s.DB.System.Exec(ctx, `DELETE FROM sessions WHERE tenant_id=$1 AND member_id=$2`, tenant, memberID)
	return err
}
