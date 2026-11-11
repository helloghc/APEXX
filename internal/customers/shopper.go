package customers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// Shopper sign-in limits.
const (
	OTPTTL         = 10 * time.Minute
	OTPMaxAttempts = 5 // wrong tries per code
	OTPPerWindow   = 3 // codes per email per OTPTTL
	SessionTTL     = 30 * 24 * time.Hour
)

// Sign-in errors.
var (
	ErrRateLimited    = errors.New("customers: too many codes requested")
	ErrBadCode        = errors.New("customers: wrong or expired code")
	ErrBadCredentials = errors.New("customers: wrong email or password")
	ErrDisabled       = errors.New("customers: account disabled")
)

func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *Service) codeHash(tenant, otpID, code string) ([]byte, error) {
	return s.Keys.Blind(tenant, "otp", otpID+"|"+code)
}

// RequestCode emails a 6-digit one-time code to email. It answers the same
// whether or not an account exists, so it cannot be used to list customers.
func (s *Service) RequestCode(ctx context.Context, tenant, email string) error {
	e, ok := NormalizeEmail(email)
	if !ok {
		return FieldError{"email", "Escribe un correo válido"}
	}
	h, err := EmailHash(s.Keys, tenant, e)
	if err != nil {
		return err
	}
	code, err := randomCode()
	if err != nil {
		return err
	}
	id := newID("otp_")
	ch, err := s.codeHash(tenant, id, code)
	if err != nil {
		return err
	}
	now := s.now()
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('otp:' || $1 || ':' || encode($2,'hex')))`, tenant, h); err != nil {
			return err
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM shopper_otps WHERE tenant_id=$1 AND email_hash=$2 AND created_at > $3`, tenant, h, now.Add(-OTPTTL)).Scan(&recent); err != nil {
			return err
		}
		if recent >= OTPPerWindow {
			return ErrRateLimited
		}
		// A new code replaces the previous ones.
		if _, err := tx.Exec(ctx, `UPDATE shopper_otps SET consumed_at=$3 WHERE tenant_id=$1 AND email_hash=$2 AND consumed_at IS NULL`, tenant, h, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shopper_otps (tenant_id, id, email_hash, code_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, id, h, ch, now.Add(OTPTTL), now); err != nil {
			return err
		}
		if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplAccessCode, To: e,
			Data: notify.Data{Vars: map[string]string{"codigo": code}}, DedupeKey: "otp:" + id}); err != nil {
			return err
		}
		return store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.code_requested"})
	})
}

func newToken() (string, []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	t := "apc_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(t))
	return t, sum[:]
}

func (s *Service) sessionTx(ctx context.Context, tx pgx.Tx, tenant, customerID string) (string, error) {
	token, sum := newToken()
	_, err := tx.Exec(ctx, `INSERT INTO shopper_sessions (tenant_id, token_hash, customer_id, expires_at) VALUES ($1,$2,$3,$4)`, tenant, sum, customerID, s.now().Add(SessionTTL))
	return token, err
}

// VerifyCode checks the code, creates the account on first use, marks the
// email as verified, links guest orders placed with that email and opens a session.
func (s *Service) VerifyCode(ctx context.Context, tenant, email, code string) (token, customerID string, err error) {
	e, ok := NormalizeEmail(email)
	code = strings.TrimSpace(code)
	if !ok || len(code) != 6 {
		return "", "", ErrBadCode
	}
	h, err := EmailHash(s.Keys, tenant, e)
	if err != nil {
		return "", "", err
	}
	var failure error
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var id string
		var want []byte
		var attempts int
		err := tx.QueryRow(ctx, `SELECT id, code_hash, attempts FROM shopper_otps WHERE tenant_id=$1 AND email_hash=$2 AND consumed_at IS NULL AND expires_at > $3
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, tenant, h, s.now()).Scan(&id, &want, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			failure = ErrBadCode
			return nil
		}
		if err != nil {
			return err
		}
		got, err := s.codeHash(tenant, id, code)
		if err != nil {
			return err
		}
		if attempts >= OTPMaxAttempts || !hmac.Equal(got, want) {
			// Count the try (committed) and burn the code once the limit is reached.
			_, err := tx.Exec(ctx, `UPDATE shopper_otps SET attempts=attempts+1, consumed_at=CASE WHEN attempts+1 >= $3 THEN now() ELSE consumed_at END
				WHERE tenant_id=$1 AND id=$2`, tenant, id, OTPMaxAttempts)
			failure = ErrBadCode
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE shopper_otps SET consumed_at=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, s.now()); err != nil {
			return err
		}
		cid, created, err := UpsertByEmailTx(ctx, tx, s.Keys, tenant, e, "", "")
		if err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, cid).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			failure = ErrDisabled
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE customers SET verified_at=COALESCE(verified_at, now()), updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, cid); err != nil {
			return err
		}
		// Guest orders with this email now belong to the verified account.
		ct, err := tx.Exec(ctx, `UPDATE orders SET customer_id=$3 WHERE tenant_id=$1 AND email_hash=$2 AND customer_id IS NULL`, tenant, h, cid)
		if err != nil {
			return err
		}
		if ct.RowsAffected() > 0 {
			if err := RecalcStatsTx(ctx, tx, tenant, cid); err != nil {
				return err
			}
		}
		if token, err = s.sessionTx(ctx, tx, tenant, cid); err != nil {
			return err
		}
		customerID = cid
		if err := store.Emit(ctx, tx, tenant, Topic, cid, map[string]any{"type": "customer.signed_in", "method": "code", "new": created, "claimedOrders": ct.RowsAffected()}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "shopper:"+cid, "customer.signed_in", cid, map[string]any{"method": "code", "claimedOrders": ct.RowsAffected()})
	})
	if err != nil {
		return "", "", err
	}
	if failure != nil {
		return "", "", failure
	}
	return token, customerID, nil
}

// dummyHash keeps the password path the same cost when the account does not exist.
var dummyHash, _ = auth.HashPassword("apexx-dummy-password")

// LoginPassword signs in a verified customer that chose a password.
func (s *Service) LoginPassword(ctx context.Context, tenant, email, password string) (token, customerID string, err error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		auth.CheckPassword(dummyHash, password)
		return "", "", ErrBadCredentials
	}
	h, err := EmailHash(s.Keys, tenant, e)
	if err != nil {
		return "", "", err
	}
	var failure error
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var hash, status string
		var verified bool
		err := tx.QueryRow(ctx, `SELECT id, password_hash, status, verified_at IS NOT NULL FROM customers WHERE tenant_id=$1 AND email_hash=$2`, tenant, h).
			Scan(&customerID, &hash, &status, &verified)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (hash == "" || !verified)) {
			auth.CheckPassword(dummyHash, password)
			failure = ErrBadCredentials
			return nil
		}
		if err != nil {
			return err
		}
		if !auth.CheckPassword(hash, password) {
			failure = ErrBadCredentials
			return nil
		}
		if status != "active" {
			failure = ErrDisabled
			return nil
		}
		if token, err = s.sessionTx(ctx, tx, tenant, customerID); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.signed_in", "method": "password"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "shopper:"+customerID, "customer.signed_in", customerID, map[string]any{"method": "password"})
	})
	if err != nil {
		return "", "", err
	}
	if failure != nil {
		return "", "", failure
	}
	return token, customerID, nil
}

// SetPassword sets the optional password of a customer ("" removes it). Other sessions are closed.
func (s *Service) SetPassword(ctx context.Context, tenant, customerID, password, keepToken string) error {
	hash := ""
	if password != "" {
		if len(password) < 10 || len(password) > 200 {
			return FieldError{"password", "La contraseña debe tener al menos 10 caracteres"}
		}
		var err error
		if hash, err = auth.HashPassword(password); err != nil {
			return err
		}
	}
	keep := sha256.Sum256([]byte(keepToken))
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE customers SET password_hash=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, customerID, hash)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM shopper_sessions WHERE tenant_id=$1 AND customer_id=$2 AND token_hash <> $3`, tenant, customerID, keep[:]); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, customerID, map[string]any{"type": "customer.password_changed", "set": hash != ""}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "shopper:"+customerID, "customer.password_changed", customerID, map[string]any{"set": hash != ""})
	})
}

// Session resolves a shopper token to its customer id.
func (s *Service) Session(ctx context.Context, tenant, token string) (string, error) {
	if !strings.HasPrefix(token, "apc_") {
		return "", ErrNotFound
	}
	sum := sha256.Sum256([]byte(token))
	var id string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT s.customer_id FROM shopper_sessions s JOIN customers c ON c.tenant_id=s.tenant_id AND c.id=s.customer_id
			WHERE s.tenant_id=$1 AND s.token_hash=$2 AND s.expires_at > $3 AND c.status='active'`, tenant, sum[:], s.now()).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return id, err
}

// Logout closes a shopper session.
func (s *Service) Logout(ctx context.Context, tenant, token string) error {
	sum := sha256.Sum256([]byte(token))
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM shopper_sessions WHERE tenant_id=$1 AND token_hash=$2`, tenant, sum[:])
		return err
	})
}

// Purge deletes expired sessions and old one-time codes of every store.
func (s *Service) Purge(ctx context.Context) error {
	if _, err := s.DB.System.Exec(ctx, `DELETE FROM shopper_sessions WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.DB.System.Exec(ctx, `DELETE FROM shopper_otps WHERE created_at < now() - interval '1 day'`)
	return err
}

// Subscribe records marketing consent for email (newsletter form). It creates
// the customer when it does not exist and never removes an existing consent.
func (s *Service) Subscribe(ctx context.Context, tenant, email, source string) error {
	e, ok := NormalizeEmail(email)
	if !ok {
		return FieldError{"email", "Escribe un correo válido"}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		id, _, err := UpsertByEmailTx(ctx, tx, s.Keys, tenant, e, "", "")
		if err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `UPDATE customers SET marketing_consent=true, consent_at=now(), consent_source=$3, version=version+1, updated_at=now()
			WHERE tenant_id=$1 AND id=$2 AND NOT marketing_consent`, tenant, id, source)
		if err != nil || ct.RowsAffected() == 0 {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.subscribed", "source": source}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "storefront:web", "customer.subscribed", id, map[string]any{"source": source})
	})
}
