package store

import (
	basestore "github.com/adonese/noebs/store"
	"github.com/jmoiron/sqlx"
)

// Store provides wallet-specific SQL access.
type Store struct {
	DB *basestore.DB
}

func New(db *basestore.DB) *Store {
	return &Store{DB: db}
}

func (s *Store) ensureDB() (*sqlx.DB, error) {
	if s == nil || s.DB == nil || s.DB.DB == nil {
		return nil, ErrMissingStore
	}
	return s.DB.DB, nil
}
