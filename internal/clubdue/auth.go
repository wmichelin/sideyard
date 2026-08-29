package clubdue

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "clubdue_session"
	sessionTTL        = 14 * 24 * time.Hour
)

type User struct {
	ID       string
	ShopID   string
	Email    string
	Role     string
	ShopName string
}

type Auth struct {
	db            *DB
	sessionSecret []byte
	secureCookies bool
	clock         Clock
}

func newAuth(db *DB, sessionSecret string, secureCookies bool) *Auth {
	return newAuthWithClock(db, sessionSecret, secureCookies, nil)
}

func newAuthWithClock(db *DB, sessionSecret string, secureCookies bool, clock Clock) *Auth {
	return &Auth{db: db, sessionSecret: []byte(sessionSecret), secureCookies: secureCookies, clock: clock}
}

func (a *Auth) nowUTC() time.Time {
	return utcNow(a.clock)
}

func (a *Auth) sessionExpiry() time.Time {
	return a.nowUTC().Add(sessionTTL)
}

func bcryptGenerate(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (a *Auth) bootstrapShop(ctx context.Context, credentials OwnerCredentials) error {
	credentials.Email = normalizeEmail(credentials.Email)
	credentials.ShopName = strings.TrimSpace(credentials.ShopName)
	if credentials.Email == "" || credentials.Password == "" {
		return nil
	}
	if credentials.ShopName == "" {
		credentials.ShopName = "Demo Shop"
	}

	var count int
	if err := a.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	_, err := a.provisionOwner(ctx, credentials)
	return err
}

func (a *Auth) provisionOwner(ctx context.Context, credentials OwnerCredentials) (string, error) {
	credentials.Email = normalizeEmail(credentials.Email)
	credentials.ShopName = strings.TrimSpace(credentials.ShopName)
	if credentials.Email == "" || credentials.Password == "" || credentials.ShopName == "" {
		return "", errors.New("owner email, password, and shop name are required")
	}
	if len(credentials.Password) < 8 {
		return "", errors.New("owner password must be at least 8 characters")
	}

	hash, err := bcryptGenerate(credentials.Password)
	if err != nil {
		return "", err
	}
	tx, err := a.db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID, shopID string
	err = tx.QueryRow(ctx, `SELECT id, shop_id FROM users WHERE email = $1 FOR UPDATE`, credentials.Email).Scan(&userID, &shopID)
	action := "recovered"
	if errors.Is(err, pgx.ErrNoRows) {
		slug := slugify(credentials.ShopName)
		err = tx.QueryRow(ctx, `SELECT id FROM shops WHERE slug = $1`, slug).Scan(&shopID)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx, `INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`, credentials.ShopName, slug).Scan(&shopID); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users (shop_id, email, password_hash, role) VALUES ($1, $2, $3, 'owner') RETURNING id`, shopID, credentials.Email, hash).Scan(&userID); err != nil {
			return "", err
		}
		action = "provisioned"
	} else if err != nil {
		return "", err
	} else {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, role = 'owner' WHERE id = $1`, userID, hash); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
			return "", err
		}
	}

	if _, err := tx.Exec(ctx, `INSERT INTO owner_provision_events (user_id, action) VALUES ($1, $2)`, userID, action); err != nil {
		return "", err
	}
	if action == "provisioned" {
		if err := recordPilotActivationTx(ctx, tx, shopID, pilotEventInvited); err != nil {
			return "", err
		}
		if err := recordPilotActivationTx(ctx, tx, shopID, pilotEventAccountCreated); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return action, nil
}

func (a *Auth) signup(ctx context.Context, shopName, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	shopName = strings.TrimSpace(shopName)
	if shopName == "" || email == "" || password == "" {
		return "", errors.New("shop name, email, and password are required")
	}
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}

	hash, err := bcryptGenerate(password)
	if err != nil {
		return "", err
	}

	slug := slugify(shopName)
	tx, err := a.db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var shopID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`,
		shopName, slug,
	).Scan(&shopID); err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return "", errors.New("shop name already taken")
		}
		return "", err
	}

	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (shop_id, email, password_hash, role)
		VALUES ($1, $2, $3, 'owner')
		RETURNING id`, shopID, email, hash,
	).Scan(&userID); err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return "", errors.New("email already registered")
		}
		return "", err
	}
	if err := recordPilotActivationTx(ctx, tx, shopID, pilotEventAccountCreated); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	return a.createSession(ctx, userID)
}

func (a *Auth) login(ctx context.Context, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var userID, hash string
	err := a.db.Pool.QueryRow(ctx, `
		SELECT u.id, u.password_hash
		FROM users u
		WHERE u.email = $1
		LIMIT 1`, email).Scan(&userID, &hash)
	if err != nil {
		return "", errors.New("invalid email or password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", errors.New("invalid email or password")
	}
	// Every successful authentication receives a new, independent session token.
	// Do not revoke the user's other sessions here: deployment smoke tests and
	// concurrent signed-in devices use the same account.
	return a.createSession(ctx, userID)
}

func (a *Auth) createSession(ctx context.Context, userID string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	tokenHash := hashToken(token, a.sessionSecret)
	expires := a.sessionExpiry()

	_, err = a.db.Pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expires,
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

func (a *Auth) logout(ctx context.Context, token string) {
	if token == "" {
		return
	}
	tokenHash := hashToken(token, a.sessionSecret)
	_, _ = a.db.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
}

func (a *Auth) userFromRequest(ctx context.Context, r *http.Request) (*User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("unauthenticated")
	}
	tokenHash := hashToken(cookie.Value, a.sessionSecret)

	var u User
	err = a.db.Pool.QueryRow(ctx, `
		SELECT u.id, u.shop_id, u.email, u.role, s.name
		FROM sessions sess
		JOIN users u ON u.id = sess.user_id
		JOIN shops s ON s.id = u.shop_id
		WHERE sess.token_hash = $1 AND sess.expires_at > $2
		`, tokenHash, a.nowUTC()).Scan(&u.ID, &u.ShopID, &u.Email, &u.Role, &u.ShopName)
	if err != nil {
		return nil, errors.New("unauthenticated")
	}
	return &u, nil
}

func (a *Auth) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		MaxAge:   -1,
	})
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string, secret []byte) string {
	h := sha256.Sum256(append(secret, []byte(token)...))
	return hex.EncodeToString(h[:])
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "shop"
	}
	return out
}
