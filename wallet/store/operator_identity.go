package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
)

// OperatorIdentity is the wallet ledger's numeric audit projection of an
// immutable OIDC identity. It carries no authorization state.
type OperatorIdentity struct {
	ID        int64     `db:"id"`
	Issuer    string    `db:"issuer"`
	Subject   string    `db:"subject"`
	CreatedAt time.Time `db:"created_at"`
}

func (s *Store) ResolveOperatorIdentity(ctx context.Context, issuer, subject string) (*OperatorIdentity, error) {
	if err := validateOperatorIdentity(issuer, subject); err != nil {
		return nil, err
	}
	if existing, err := s.GetOperatorIdentity(ctx, issuer, subject); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrOperatorIdentityNotFound) {
		return nil, err
	}

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`INSERT INTO operator_identities(issuer, subject)
		VALUES(?, ?)
		ON CONFLICT (issuer, subject) DO NOTHING
		RETURNING id, issuer, subject, created_at`)
	var stored OperatorIdentity
	if err := db.GetContext(ctx, &stored, stmt, issuer, subject); err == nil {
		return &stored, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A concurrent resolver won the insert. This new statement observes its
	// committed row without turning every warm lookup into a physical update.
	return s.GetOperatorIdentity(ctx, issuer, subject)
}

func (s *Store) GetOperatorIdentity(ctx context.Context, issuer, subject string) (*OperatorIdentity, error) {
	if err := validateOperatorIdentity(issuer, subject); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT id, issuer, subject, created_at
		FROM operator_identities WHERE issuer = ? AND subject = ?`)
	var operator OperatorIdentity
	if err := db.GetContext(ctx, &operator, stmt, issuer, subject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOperatorIdentityNotFound
		}
		return nil, err
	}
	return &operator, nil
}

func (s *Store) GetOperatorIdentityByID(ctx context.Context, operatorID int64) (*OperatorIdentity, error) {
	if operatorID <= 0 {
		return nil, ErrMissingOperatorID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT id, issuer, subject, created_at
		FROM operator_identities WHERE id = ?`)
	var operator OperatorIdentity
	if err := db.GetContext(ctx, &operator, stmt, operatorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOperatorIdentityNotFound
		}
		return nil, err
	}
	return &operator, nil
}

func validateOperatorIdentity(issuer, subject string) error {
	if issuer == "" {
		return ErrMissingOperatorIssuer
	}
	if issuer != strings.TrimSpace(issuer) || strings.IndexFunc(issuer, unicode.IsControl) >= 0 {
		return ErrInvalidOperatorIssuer
	}
	if subject == "" {
		return ErrMissingOperatorSubject
	}
	if subject != strings.TrimSpace(subject) || strings.IndexFunc(subject, unicode.IsControl) >= 0 {
		return ErrInvalidOperatorSubject
	}
	return nil
}
