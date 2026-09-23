package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

var (
	ErrInvalidCredentials           = errors.New("invalid credentials")
	ErrUsernameTaken                = errors.New("username already exists")
	ErrInvalidRegistration          = errors.New("invalid registration")
	ErrRegistrationEmailDomain      = errors.New("registration email domain is not allowed")
	ErrPasswordMismatch             = errors.New("current password is invalid")
	ErrSessionInvalid               = errors.New("session is invalid")
	ErrEmailVerificationRequired    = errors.New("email verification is required")
	ErrEmailVerificationExpired     = errors.New("email verification has expired")
	ErrEmailVerificationInvalid     = errors.New("email verification is invalid")
	ErrEmailVerificationRateLimited = errors.New("email verification is rate limited")
)

const (
	registrationEmailVerificationTTL      = 10 * time.Minute
	registrationEmailVerificationCooldown = time.Minute
)

type emailVerification struct {
	code      string
	sentAt    time.Time
	expiresAt time.Time
}

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// Service owns user credentials and server-side sessions.
type Service struct {
	ttl                      time.Duration
	pool                     *pgxpool.Pool
	artifactRoot             string
	registrationEmailDomains map[string]struct{}
	verificationMu           sync.Mutex
	emailVerifications       map[string]emailVerification
}

// NewPostgresService uses PostgreSQL for users and server-side sessions.
func NewPostgresService(ctx context.Context, pool *pgxpool.Pool, artifactRoot string, ttl time.Duration, registrationEmailDomains []string) (*Service, error) {
	if pool == nil {
		return nil, errors.New("database is required")
	}
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	allowedDomains := make(map[string]struct{})
	for _, domain := range registrationEmailDomains {
		allowedDomains[strings.ToLower(strings.TrimSpace(domain))] = struct{}{}
	}
	return &Service{
		pool: pool, artifactRoot: filepath.Clean(artifactRoot), ttl: ttl,
		registrationEmailDomains: allowedDomains,
		emailVerifications:       make(map[string]emailVerification),
	}, nil
}

// SendRegistrationEmailVerification checks registration identifiers before
// creating a short-lived code. The code remains process-local until a real
// mail delivery system is added.
func (s *Service) SendRegistrationEmailVerification(ctx context.Context, username, email string) (usernameAvailable, emailAvailable bool, resendAfterSeconds uint32, err error) {
	username = normalizeUsername(username)
	email = normalizeEmail(email)
	if !validUsername(username) {
		return false, false, 0, ErrInvalidRegistration
	}
	if err := s.validateRegistrationEmail(email); err != nil {
		return false, false, 0, err
	}
	usernameAvailable, emailAvailable, err = s.registrationIdentifiersAvailable(ctx, username, email)
	if err != nil || !usernameAvailable || !emailAvailable {
		return usernameAvailable, emailAvailable, 0, err
	}
	resendAfterSeconds, err = s.issueRegistrationEmailVerification(email)
	return true, true, resendAfterSeconds, err
}

func (s *Service) registrationIdentifiersAvailable(ctx context.Context, username, email string) (bool, bool, error) {
	var usernameAvailable, emailAvailable bool
	err := s.pool.QueryRow(ctx, `SELECT
		NOT EXISTS (SELECT 1 FROM users WHERE username=$1),
		NOT EXISTS (SELECT 1 FROM users WHERE email=$2)`, username, email).Scan(&usernameAvailable, &emailAvailable)
	return usernameAvailable, emailAvailable, err

}

func (s *Service) issueRegistrationEmailVerification(email string) (uint32, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return 0, ErrInvalidRegistration
	}
	now := time.Now()
	s.verificationMu.Lock()
	defer s.verificationMu.Unlock()
	for key, verification := range s.emailVerifications {
		if !verification.expiresAt.After(now) {
			delete(s.emailVerifications, key)
		}
	}
	if verification, ok := s.emailVerifications[email]; ok && now.Before(verification.sentAt.Add(registrationEmailVerificationCooldown)) {
		return 0, ErrEmailVerificationRateLimited
	}
	code, err := randomEmailVerificationCode()
	if err != nil {
		return 0, err
	}
	s.emailVerifications[email] = emailVerification{
		code: code, sentAt: now, expiresAt: now.Add(registrationEmailVerificationTTL),
	}
	log.Printf("registration email verification code email=%s code=%s", email, code)
	return uint32(registrationEmailVerificationCooldown / time.Second), nil
}

func randomEmailVerificationCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *Service) Register(ctx context.Context, username, email, password, verificationCode string) (User, error) {
	username = normalizeUsername(username)
	email = normalizeEmail(email)
	if !validUsername(username) || !validPassword(password) {
		return User{}, ErrInvalidRegistration
	}
	if err := s.validateRegistrationEmail(email); err != nil {
		return User{}, err
	}
	if err := s.consumeRegistrationEmailVerification(email, verificationCode); err != nil {
		return User{}, err
	}
	usernameAvailable, emailAvailable, err := s.registrationIdentifiersAvailable(ctx, username, email)
	if err != nil {
		return User{}, err
	}
	if !usernameAvailable || !emailAvailable {
		return User{}, ErrUsernameTaken
	}
	return s.registerPostgres(ctx, username, email, password)
}

func (s *Service) validRegistrationEmailVerification(email, code string) error {
	if len(code) != 6 {
		return ErrEmailVerificationInvalid
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return ErrEmailVerificationInvalid
		}
	}
	now := time.Now()
	s.verificationMu.Lock()
	defer s.verificationMu.Unlock()
	return s.registrationEmailVerificationErrorLocked(email, code, now)
}

func (s *Service) consumeRegistrationEmailVerification(email, code string) error {
	if len(code) != 6 {
		return ErrEmailVerificationInvalid
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return ErrEmailVerificationInvalid
		}
	}
	now := time.Now()
	s.verificationMu.Lock()
	defer s.verificationMu.Unlock()
	if err := s.registrationEmailVerificationErrorLocked(email, code, now); err != nil {
		return err
	}
	verification := s.emailVerifications[email]
	verification.code = ""
	s.emailVerifications[email] = verification
	return nil
}

func (s *Service) registrationEmailVerificationErrorLocked(email, code string, now time.Time) error {
	verification, ok := s.emailVerifications[email]
	if !ok {
		return ErrEmailVerificationRequired
	}
	if !verification.expiresAt.After(now) {
		delete(s.emailVerifications, email)
		return ErrEmailVerificationExpired
	}
	if verification.code == "" {
		return ErrEmailVerificationRequired
	}
	if subtle.ConstantTimeCompare([]byte(verification.code), []byte(code)) != 1 {
		return ErrEmailVerificationInvalid
	}
	return nil
}

// LoginSession validates credentials and creates an independent session while
// holding the user lock, so password changes cannot race a stale login.
func (s *Service) LoginSession(ctx context.Context, username, password string) (User, string, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var u User
	var hash string
	err = tx.QueryRow(ctx, `SELECT id,username,email,role,status,created_at,password_hash FROM users WHERE username=$1 FOR UPDATE`, normalizeUsername(username)).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Status, &u.CreatedAt, &hash)
	if err != nil || u.Status != "active" || !verifyPassword(ctx, hash, password) {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}
	if passwordNeedsRehash(hash) {
		hash, err = hashPassword(ctx, password)
		if err != nil {
			return User{}, "", time.Time{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, u.ID, hash); err != nil {
			return User{}, "", time.Time{}, err
		}
	}
	token, expires, err := s.insertSession(ctx, tx, u.ID)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return u, token, expires, err
}

func (s *Service) insertSession(ctx context.Context, tx pgx.Tx, userID int64) (string, time.Time, error) {
	token, err := randomSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	hash, ok := sessionTokenHash(token)
	if !ok {
		return "", time.Time{}, ErrSessionInvalid
	}
	expires := time.Now().Add(s.ttl)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3) RETURNING id`, hash, userID, expires).Scan(&id)
	return token, expires, err
}

// CreateSession is used after account creation; login uses LoginSession.
func (s *Service) CreateSession(ctx context.Context, user User) (string, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, `SELECT status='active' FROM users WHERE id=$1 FOR UPDATE`, user.ID).Scan(&active); err != nil || !active {
		return "", time.Time{}, ErrSessionInvalid
	}
	token, expires, err := s.insertSession(ctx, tx, user.ID)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return token, expires, err
}

func randomSessionToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func sessionTokenHash(token string) (string, bool) {
	if len(token) != 36 || token[8] != '-' || token[13] != '-' || token[18] != '-' || token[23] != '-' {
		return "", false
	}
	encoded := strings.ReplaceAll(token, "-", "")
	bytes, err := hex.DecodeString(encoded)
	if err != nil || len(bytes) != 16 {
		return "", false
	}
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:]), true
}

func (s *Service) CurrentSession(ctx context.Context, token string) (User, time.Time, error) {
	var u User
	var expires time.Time
	hash, ok := sessionTokenHash(token)
	if !ok {
		return User{}, time.Time{}, ErrSessionInvalid
	}
	err := s.pool.QueryRow(ctx, `SELECT u.id,u.username,u.email,u.role,u.status,u.created_at,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.status='active'`, hash).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Status, &u.CreatedAt, &expires)
	return u, expires, err
}
func (s *Service) CurrentUser(ctx context.Context, token string) (User, bool) {
	u, _, err := s.CurrentSession(ctx, token)
	return u, err == nil
}
func (s *Service) Revoke(ctx context.Context, token string) error {
	hash, ok := sessionTokenHash(token)
	if !ok {
		return ErrSessionInvalid
	}
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1`, hash)
	return err
}

// Always lock user before session. Revalidate the session after obtaining locks.
func (s *Service) lockSession(ctx context.Context, tx pgx.Tx, token string) (int64, int64, error) {
	hash, ok := sessionTokenHash(token)
	if !ok {
		return 0, 0, ErrSessionInvalid
	}
	var userID int64
	err := tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token_hash=$1 AND u.status='active' FOR UPDATE OF u`, hash).Scan(&userID)
	if err != nil {
		return 0, 0, ErrSessionInvalid
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, hash).Scan(&id)
	if err != nil {
		return 0, 0, ErrSessionInvalid
	}
	return userID, id, nil
}
func (s *Service) RefreshSession(ctx context.Context, token string) (string, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(ctx)
	user, id, err := s.lockSession(ctx, tx, token)
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, id); err != nil {
		return "", time.Time{}, err
	}
	next, expires, err := s.insertSession(ctx, tx, user)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return next, expires, err
}
func (s *Service) ChangePassword(ctx context.Context, token, currentPassword, newPassword string) error {
	if _, ok := sessionTokenHash(token); !ok {
		return ErrSessionInvalid
	}
	if !validPassword(newPassword) || currentPassword == newPassword {
		return ErrInvalidRegistration
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	user, _, err := s.lockSession(ctx, tx, token)
	if err != nil {
		return err
	}
	var oldHash string
	if err = tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, user).Scan(&oldHash); err != nil {
		return err
	}
	if !verifyPassword(ctx, oldHash, currentPassword) {
		return ErrPasswordMismatch
	}
	newHash, err := hashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, user, newHash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) registerPostgres(ctx context.Context, username, email, password string) (User, error) {
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return User{}, err
	}
	user := User{Username: username, Email: email, Role: RoleUser, Status: "active"}
	err = s.pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role, status) VALUES ($1, $2, $3, 'user', 'active') RETURNING id,created_at`, user.Username, user.Email, hash).Scan(&user.ID, &user.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	return user, nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `SELECT id,username,email,role,status,created_at FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Status, &u.CreatedAt)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validUsername(value string) bool {
	if len(value) < 3 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validEmail(value string) bool {
	if len(value) == 0 || len(value) > 254 || !strings.Contains(value, "@") {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func (s *Service) validateRegistrationEmail(email string) error {
	if !validEmail(email) {
		return ErrInvalidRegistration
	}
	if len(s.registrationEmailDomains) == 0 {
		return nil
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return ErrInvalidRegistration
	}
	_, allowed := s.registrationEmailDomains[domain]
	if !allowed {
		return ErrRegistrationEmailDomain
	}
	return nil
}
