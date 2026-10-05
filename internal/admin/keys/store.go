package keys

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/statefile"
)

const maxKeys = 32

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Config creates a Store.
type Config struct {
	Path  string
	Clock func() time.Time
	Rand  io.Reader
}

// Store manages hashed admin API keys on disk.
type Store struct {
	path  string
	clock func() time.Time
	rand  io.Reader
}

// Record is one key metadata row (no plaintext).
type Record struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Hash      string `json:"hash"`
	CreatedAt string `json:"created_at"`
	RotatedAt string `json:"rotated_at,omitempty"`
}

type filePayload struct {
	Version int      `json:"version"`
	Keys    []Record `json:"keys"`
}

// CreateStore opens the key store at cfg.Path.
func (cfg Config) CreateStore() (*Store, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, derrors.New(derrors.CodeInvalid, "keys.CreateStore", "path required")
	}
	if cfg.Clock == nil {
		return nil, derrors.New(derrors.CodeInvalid, "keys.CreateStore", "clock required")
	}
	r := cfg.Rand
	if r == nil {
		r = rand.Reader
	}
	return &Store{path: cfg.Path, clock: cfg.Clock, rand: r}, nil
}

// GenerateResult holds a one-time plaintext key.
type GenerateResult struct {
	Record Record
	Key    string
}

// Generate adds a new named key.
func (s *Store) Generate(name string) (GenerateResult, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return GenerateResult{}, derrors.New(derrors.CodeInvalid, "keys.Generate", "invalid name")
	}
	var out GenerateResult
	err := statefile.WithFileLock(s.path, func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		if len(file.Keys) >= maxKeys {
			return derrors.New(derrors.CodeFailedPrecondition, "keys.Generate", "max keys reached")
		}
		for _, k := range file.Keys {
			if k.Name == name {
				return derrors.New(derrors.CodeConflict, "keys.Generate", "name already exists")
			}
		}
		id, secret, token, err := s.newToken()
		if err != nil {
			return err
		}
		hash, err := hashSecret([]byte(secret))
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "keys.Generate", "hash")
		}
		rec := Record{
			ID:        id,
			Name:      name,
			Prefix:    "ppk." + id,
			Hash:      hash,
			CreatedAt: s.clock().UTC().Format(time.RFC3339),
		}
		file.Keys = append(file.Keys, rec)
		if err := s.write(file); err != nil {
			return err
		}
		out = GenerateResult{Record: rec, Key: token}
		return nil
	})
	return out, err
}

// Rotate replaces the secret for an existing name.
func (s *Store) Rotate(name string) (GenerateResult, error) {
	name = strings.TrimSpace(name)
	var out GenerateResult
	err := statefile.WithFileLock(s.path, func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		idx := -1
		for i, k := range file.Keys {
			if k.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return derrors.New(derrors.CodeNotFound, "keys.Rotate", "key not found")
		}
		secret, err := s.newSecretBytes()
		if err != nil {
			return err
		}
		token := "ppk." + file.Keys[idx].ID + "." + secret
		hash, err := hashSecret([]byte(secret))
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "keys.Rotate", "hash")
		}
		file.Keys[idx].Hash = hash
		file.Keys[idx].RotatedAt = s.clock().UTC().Format(time.RFC3339)
		if err := s.write(file); err != nil {
			return err
		}
		out = GenerateResult{Record: file.Keys[idx], Key: token}
		return nil
	})
	return out, err
}

// Delete removes a key by name.
func (s *Store) Delete(name string) error {
	name = strings.TrimSpace(name)
	return statefile.WithFileLock(s.path, func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		var kept []Record
		found := false
		for _, k := range file.Keys {
			if k.Name == name {
				found = true
				continue
			}
			kept = append(kept, k)
		}
		if !found {
			return derrors.New(derrors.CodeNotFound, "keys.Delete", "key not found")
		}
		file.Keys = kept
		return s.write(file)
	})
}

// List returns metadata for all keys.
func (s *Store) List() ([]Record, error) {
	file, err := s.readFresh()
	if err != nil {
		return nil, err
	}
	return append([]Record(nil), file.Keys...), nil
}

// Verify checks a bearer token against the on-disk store (re-reads each call).
func (s *Store) Verify(token string) (Record, error) {
	id, secret, ok := parseToken(token)
	if !ok {
		return Record{}, derrors.New(derrors.CodeUnauthorized, "keys.Verify", "invalid token")
	}
	file, err := s.readFresh()
	if err != nil {
		return Record{}, err
	}
	for _, k := range file.Keys {
		if k.ID != id {
			continue
		}
		if verifyHash(k.Hash, []byte(secret)) {
			return k, nil
		}
		return Record{}, derrors.New(derrors.CodeUnauthorized, "keys.Verify", "invalid token")
	}
	return Record{}, derrors.New(derrors.CodeUnauthorized, "keys.Verify", "invalid token")
}

func (s *Store) readFresh() (filePayload, error) {
	return s.read()
}

func (s *Store) read() (filePayload, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return filePayload{Version: 1, Keys: nil}, nil
		}
		return filePayload{}, derrors.Wrap(err, derrors.CodeInternal, "keys.read", "read")
	}
	var file filePayload
	if err := json.Unmarshal(raw, &file); err != nil {
		return filePayload{}, derrors.Wrap(err, derrors.CodeInternal, "keys.read", "parse")
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

func (s *Store) write(file filePayload) error {
	file.Version = 1
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "keys.write", "marshal")
	}
	return statefile.WriteAtomic(s.path, raw, 0o600)
}

func (s *Store) newToken() (id string, secret string, token string, err error) {
	idBytes := make([]byte, 16)
	if _, err := io.ReadFull(s.rand, idBytes); err != nil {
		return "", "", "", derrors.Wrap(err, derrors.CodeInternal, "keys.newToken", "id rand")
	}
	id = hex.EncodeToString(idBytes)
	secret, token, err = s.secretForID(id)
	return id, secret, token, err
}

func (s *Store) newSecretBytes() (secret string, err error) {
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, secretBytes); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, "keys.newSecretBytes", "secret rand")
	}
	return base64.RawURLEncoding.EncodeToString(secretBytes), nil
}

func (s *Store) secretForID(id string) (secret string, token string, err error) {
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, secretBytes); err != nil {
		return "", "", derrors.Wrap(err, derrors.CodeInternal, "keys.secretForID", "secret rand")
	}
	secret = base64.RawURLEncoding.EncodeToString(secretBytes)
	token = "ppk." + id + "." + secret
	return secret, token, nil
}

func parseToken(token string) (id, secret string, ok bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "ppk.") {
		return "", "", false
	}
	rest := strings.TrimPrefix(token, "ppk.")
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
